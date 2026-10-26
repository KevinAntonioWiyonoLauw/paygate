package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
)

// PaymentRepo persists provider transactions.
type PaymentRepo struct{ store *Store }

// Payments returns the payment repository.
func (s *Store) Payments() *PaymentRepo { return &PaymentRepo{store: s} }

// Upsert inserts a transaction, or returns the existing row when the provider
// re-delivers it. The unique (provider, provider_tx_id) constraint is what makes
// reconciliation idempotent across restarts.
//
// The bool result reports whether a new row was created.
func (r *PaymentRepo) Upsert(ctx context.Context, p domain.Payment) (domain.Payment, bool, error) {
	raw := p.Raw
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var created bool
	row := r.store.pool.QueryRow(ctx, `
		INSERT INTO payments (provider, provider_tx_id, gross_amount, real_amount,
			status, tx_time, raw)
		VALUES ($1,$2,$3,NULLIF($4,0),$5,$6,$7)
		ON CONFLICT (provider, provider_tx_id) DO NOTHING
		RETURNING id, created_at`,
		p.Provider, p.ProviderTxID, p.GrossAmount, p.RealAmount, p.Status, p.TxTime, raw)

	var id int64
	var createdAt time.Time
	err := row.Scan(&id, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already present: fetch it so callers get the canonical row.
		existing, getErr := r.GetByProviderTxID(ctx, p.Provider, p.ProviderTxID)
		return existing, false, getErr
	}
	if err != nil {
		return domain.Payment{}, false, fmt.Errorf("payment: upsert: %w", err)
	}
	created = true

	got, err := r.GetByProviderTxID(ctx, p.Provider, p.ProviderTxID)
	if err != nil {
		return domain.Payment{}, created, err
	}
	return got, created, nil
}

const paymentColumns = `
	p.id, p.provider, p.provider_tx_id, p.gross_amount, COALESCE(p.real_amount,0),
	p.status, p.tx_time, p.order_id, COALESCE(o.public_id, ''), p.match_rule,
	p.raw, p.created_at, p.updated_at`

func scanPayment(row pgx.Row) (domain.Payment, error) {
	var p domain.Payment
	var raw []byte
	err := row.Scan(&p.ID, &p.Provider, &p.ProviderTxID, &p.GrossAmount, &p.RealAmount,
		&p.Status, &p.TxTime, &p.OrderID, &p.OrderPublicID, &p.MatchRule,
		&raw, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Raw = raw
	return p, nil
}

// GetByProviderTxID fetches one transaction.
func (r *PaymentRepo) GetByProviderTxID(ctx context.Context, provider, txID string) (domain.Payment, error) {
	row := r.store.pool.QueryRow(ctx, `
		SELECT `+paymentColumns+`
		FROM payments p LEFT JOIN orders o ON o.id = p.order_id
		WHERE p.provider = $1 AND p.provider_tx_id = $2`, provider, txID)
	p, err := scanPayment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, domain.ErrNotFound
	}
	return p, err
}

// ListFilter narrows the payment listing.
type PaymentListFilter struct {
	UnmatchedOnly bool
	Status        []domain.PaymentStatus
	Limit         int
	Offset        int
}

// List returns transactions newest first.
func (r *PaymentRepo) List(ctx context.Context, f PaymentListFilter) ([]domain.Payment, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{}
	where := "WHERE 1=1"
	if f.UnmatchedOnly {
		where += " AND p.order_id IS NULL"
	}
	if len(f.Status) > 0 {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND p.status = ANY($%d)", len(args))
	}

	var total int
	if err := r.store.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("payment: count: %w", err)
	}

	args = append(args, limit, f.Offset)
	rows, err := r.store.pool.Query(ctx, `
		SELECT `+paymentColumns+`
		FROM payments p LEFT JOIN orders o ON o.id = p.order_id `+where+
		fmt.Sprintf(` ORDER BY p.created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("payment: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Payment, 0, limit)
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// SetStatus updates a payment's status.
func (r *PaymentRepo) SetStatus(ctx context.Context, id int64, status domain.PaymentStatus) error {
	_, err := r.store.pool.Exec(ctx,
		`UPDATE payments SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("payment: set status: %w", err)
	}
	return nil
}

// MatchManually attributes an unmatched payment to an order and settles it.
func (r *PaymentRepo) MatchManually(ctx context.Context, paymentID, orderID int64, actor string) error {
	return r.store.inTx(ctx, func(tx pgx.Tx) error {
		var status domain.OrderStatus
		if err := tx.QueryRow(ctx,
			`SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		if status == domain.OrderPaid {
			return fmt.Errorf("%w: order already paid", domain.ErrConflict)
		}
		if status == domain.OrderCancelled {
			return fmt.Errorf("%w: order was cancelled", domain.ErrConflict)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE payments SET order_id = $1, match_rule = $2, updated_at = now() WHERE id = $3`,
			orderID, int(domain.RuleManual), paymentID); err != nil {
			return fmt.Errorf("payment: manual match: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET status = 'paid', paid_at = now(), updated_at = now() WHERE id = $1`,
			orderID); err != nil {
			return fmt.Errorf("payment: settle order: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE amount_reservations SET last_used_at = now() WHERE order_id = $1`,
			orderID); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"payment_id": paymentID, "order_id": orderID})
		if _, err := tx.Exec(ctx,
			`INSERT INTO audit_log (actor, action, entity, entity_id, detail)
			 VALUES ($1,'manual_match','payment',$2,$3)`,
			actor, fmt.Sprint(paymentID), detail); err != nil {
			return err
		}
		return nil
	})
}

// UnmatchedOlderThan returns suspense rows older than the cutoff, for alerting.
func (r *PaymentRepo) UnmatchedOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	var n int
	err := r.store.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id IS NULL AND created_at < $1`, cutoff).Scan(&n)
	return n, err
}
