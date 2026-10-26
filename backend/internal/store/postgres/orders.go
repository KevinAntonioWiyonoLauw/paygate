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

// OrderRepo persists orders.
type OrderRepo struct{ store *Store }

// Orders returns the order repository.
func (s *Store) Orders() *OrderRepo { return &OrderRepo{store: s} }

const orderColumns = `
	id, public_id, base_amount, unique_offset, total_amount, status, source,
	provider, qr_payload, COALESCE(reference, ''), COALESCE(description, ''),
	metadata, expires_at, paid_at, created_at, updated_at`

func scanOrder(row pgx.Row) (domain.Order, error) {
	var o domain.Order
	var meta []byte
	err := row.Scan(
		&o.ID, &o.PublicID, &o.BaseAmount, &o.UniqueOffset, &o.TotalAmount,
		&o.Status, &o.Source, &o.Provider, &o.QRPayload, &o.Reference,
		&o.Description, &meta, &o.ExpiresAt, &o.PaidAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return o, err
	}
	o.Metadata = map[string]any{}
	if len(meta) > 0 {
		// The column can hold JSONB `null` (written by an older insert path).
		// Unmarshalling null into a map yields a nil map, which serialises back
		// as null; normalise it so callers always get an indexable object.
		if err := json.Unmarshal(meta, &o.Metadata); err != nil || o.Metadata == nil {
			o.Metadata = map[string]any{}
		}
	}
	return o, nil
}

// Create inserts a new order. The public id must already be unique.
func (r *OrderRepo) Create(ctx context.Context, o domain.Order) (domain.Order, error) {
	// An order created without metadata must still serialise as {} rather than
	// null, so the UI can index into it without a nil check.
	if o.Metadata == nil {
		o.Metadata = map[string]any{}
	}
	meta, err := json.Marshal(o.Metadata)
	if err != nil {
		return domain.Order{}, fmt.Errorf("order: marshal metadata: %w", err)
	}
	row := r.store.pool.QueryRow(ctx, `
		INSERT INTO orders (public_id, base_amount, unique_offset, total_amount,
			status, source, provider, qr_payload, reference, description, metadata, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),$11,$12)
		RETURNING `+orderColumns,
		o.PublicID, o.BaseAmount, o.UniqueOffset, o.TotalAmount, o.Status, o.Source,
		o.Provider, o.QRPayload, o.Reference, o.Description, meta, o.ExpiresAt,
	)
	return scanOrder(row)
}

// GetByPublicID looks an order up by its caller-facing id.
func (r *OrderRepo) GetByPublicID(ctx context.Context, publicID string) (domain.Order, error) {
	row := r.store.pool.QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE public_id = $1`, publicID)
	o, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, err
}

// GetByID looks an order up by primary key.
func (r *OrderRepo) GetByID(ctx context.Context, id int64) (domain.Order, error) {
	row := r.store.pool.QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE id = $1`, id)
	o, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, err
}

// ListFilter narrows the order listing.
type ListFilter struct {
	Status  []domain.OrderStatus
	Search  string
	Limit   int
	Offset  int
	Since   *time.Time
}

// List returns orders newest first.
func (r *OrderRepo) List(ctx context.Context, f ListFilter) ([]domain.Order, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{}
	where := "WHERE 1=1"

	if len(f.Status) > 0 {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = ANY($%d)", len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where += fmt.Sprintf(" AND (public_id ILIKE $%d OR COALESCE(reference,'') ILIKE $%d OR COALESCE(description,'') ILIKE $%d)",
			len(args), len(args), len(args))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		where += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}

	var total int
	if err := r.store.pool.QueryRow(ctx,
		`SELECT count(*) FROM orders `+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("order: count: %w", err)
	}

	args = append(args, limit, f.Offset)
	rows, err := r.store.pool.Query(ctx,
		`SELECT `+orderColumns+` FROM orders `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("order: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Order, 0, limit)
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// SetStatus transitions an order, enforcing the domain state machine.
// Returns the updated order.
func (r *OrderRepo) SetStatus(ctx context.Context, id int64, to domain.OrderStatus) (domain.Order, error) {
	var updated domain.Order
	err := r.store.inTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1 FOR UPDATE`, id)
		current, err := scanOrder(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		if current.Status == to {
			updated = current
			return nil
		}
		if err := domain.ValidateTransition(current.Status, to); err != nil {
			return err
		}

		var paidAt *time.Time
		if to == domain.OrderPaid {
			now := time.Now().UTC()
			paidAt = &now
		}
		row = tx.QueryRow(ctx, `
			UPDATE orders
			SET status = $2,
			    paid_at = COALESCE($3, paid_at),
			    updated_at = now()
			WHERE id = $1
			RETURNING `+orderColumns, id, to, paidAt)
		updated, err = scanOrder(row)
		return err
	})
	return updated, err
}

// AttachPayment links a payment to an order and optionally settles it, in one
// transaction, so an order can never be marked paid without the payment row.
func (r *OrderRepo) AttachPayment(ctx context.Context, orderID, paymentID int64, rule domain.MatchRule, settle bool) (domain.Order, error) {
	var updated domain.Order
	err := r.store.inTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1 FOR UPDATE`, orderID)
		current, err := scanOrder(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}

		target := domain.OrderReview
		if settle {
			target = domain.OrderPaid
		}
		if current.Status != target {
			if err := domain.ValidateTransition(current.Status, target); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE payments SET order_id = $1, match_rule = $2, updated_at = now() WHERE id = $3`,
			orderID, int(rule), paymentID); err != nil {
			return fmt.Errorf("order: attach payment: %w", err)
		}

		var paidAt *time.Time
		if settle {
			now := time.Now().UTC()
			paidAt = &now
		}
		row = tx.QueryRow(ctx, `
			UPDATE orders
			SET status = $2, paid_at = COALESCE($3, paid_at), updated_at = now()
			WHERE id = $1
			RETURNING `+orderColumns, orderID, target, paidAt)
		updated, err = scanOrder(row)
		if err != nil {
			return err
		}

		if settle {
			if _, err := tx.Exec(ctx,
				`UPDATE amount_reservations SET last_used_at = now() WHERE order_id = $1`,
				orderID); err != nil {
				return fmt.Errorf("order: mark offset used: %w", err)
			}
		}
		return nil
	})
	return updated, err
}

// ExpireStale moves pending orders past their window to expired and releases
// their offsets. Returns the public ids that changed.
func (r *OrderRepo) ExpireStale(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.store.pool.Query(ctx, `
		WITH stale AS (
			SELECT id FROM orders
			WHERE status = 'pending' AND expires_at < now()
			ORDER BY expires_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), upd AS (
			UPDATE orders o SET status = 'expired', updated_at = now()
			FROM stale WHERE o.id = stale.id
			RETURNING o.id, o.public_id
		)
		UPDATE amount_reservations r SET reserved_until = now()
		FROM upd WHERE r.order_id = upd.id
		RETURNING upd.public_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("order: expire stale: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PendingCandidates returns payable orders whose amount could match a
// transaction amount, plus a small tolerance band. Used by the matcher.
func (r *OrderRepo) PendingCandidates(ctx context.Context, amount int64, from, to time.Time) ([]domain.Order, error) {
	rows, err := r.store.pool.Query(ctx, `
		SELECT `+orderColumns+`
		FROM orders
		WHERE total_amount = $1
		  AND status IN ('pending','expired','review')
		  AND created_at <= $3
		  AND expires_at >= $2
		ORDER BY created_at`, amount, from, to)
	if err != nil {
		return nil, fmt.Errorf("order: candidates: %w", err)
	}
	defer rows.Close()

	var out []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Stats is the dashboard aggregate.
type Stats struct {
	PaidToday      int64   `json:"paid_today"`
	RevenueToday   int64   `json:"revenue_today"`
	PendingCount   int64   `json:"pending_count"`
	PendingAmount  int64   `json:"pending_amount"`
	ReviewCount    int64   `json:"review_count"`
	UnmatchedCount int64   `json:"unmatched_count"`
	ExpiredToday   int64   `json:"expired_today"`
	Volume7d       []Daily `json:"volume_7d"`
}

// Daily is one point in the 7-day chart.
type Daily struct {
	Date    string `json:"date"`
	Count   int64  `json:"count"`
	Revenue int64  `json:"revenue"`
}

// Stats aggregates the dashboard numbers in one round trip.
func (r *OrderRepo) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	err := r.store.pool.QueryRow(ctx, `
		SELECT
			COALESCE(COUNT(*) FILTER (WHERE status = 'paid' AND paid_at >= date_trunc('day', now())), 0),
			COALESCE(SUM(total_amount) FILTER (WHERE status = 'paid' AND paid_at >= date_trunc('day', now())), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'pending'), 0),
			COALESCE(SUM(total_amount) FILTER (WHERE status = 'pending'), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'review'), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'expired' AND updated_at >= date_trunc('day', now())), 0)
		FROM orders`).Scan(
		&s.PaidToday, &s.RevenueToday, &s.PendingCount,
		&s.PendingAmount, &s.ReviewCount, &s.ExpiredToday)
	if err != nil {
		return s, fmt.Errorf("order: stats: %w", err)
	}

	if err := r.store.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id IS NULL`,
	).Scan(&s.UnmatchedCount); err != nil {
		return s, fmt.Errorf("order: unmatched count: %w", err)
	}

	rows, err := r.store.pool.Query(ctx, `
		SELECT to_char(d.day, 'YYYY-MM-DD'),
		       COALESCE(o.cnt, 0), COALESCE(o.revenue, 0)
		FROM generate_series(
			date_trunc('day', now()) - interval '6 days',
			date_trunc('day', now()),
			interval '1 day'
		) AS d(day)
		LEFT JOIN (
			SELECT date_trunc('day', paid_at) AS day,
			       count(*) AS cnt, sum(total_amount) AS revenue
			FROM orders WHERE status = 'paid' AND paid_at >= date_trunc('day', now()) - interval '6 days'
			GROUP BY 1
		) o ON o.day = d.day
		ORDER BY d.day`)
	if err != nil {
		return s, fmt.Errorf("order: volume: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d Daily
		if err := rows.Scan(&d.Date, &d.Count, &d.Revenue); err != nil {
			return s, err
		}
		s.Volume7d = append(s.Volume7d, d)
	}
	return s, rows.Err()
}
