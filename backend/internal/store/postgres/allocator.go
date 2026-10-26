package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
)

// AllocatorRepo owns the unique-amount offset pool.
type AllocatorRepo struct{ store *Store }

// Allocator returns the allocator repository.
func (s *Store) Allocator() *AllocatorRepo { return &AllocatorRepo{store: s} }

// Allocate reserves the lowest free offset for baseAmount and returns it.
// Returns domain.ErrPoolExhausted when every slot in the window is taken.
//
// The candidate selection and the insert happen in one statement, so two
// concurrent callers cannot both win the same offset: the primary key
// (base_amount, slot) rejects the loser, and the caller retries.
func (r *AllocatorRepo) Allocate(ctx context.Context, baseAmount int64, orderID int64, maxOffset int, ttl time.Duration, cooldown time.Duration) (int, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var offset int
		err := r.store.pool.QueryRow(ctx, `
			INSERT INTO amount_reservations (base_amount, slot, order_id, reserved_until)
			SELECT $1::bigint, candidate, NULLIF($2::bigint, 0), now() + $4::interval
			FROM generate_series(1, $3::int) AS candidate
			WHERE NOT EXISTS (
				SELECT 1 FROM amount_reservations r
				WHERE r.base_amount = $1::bigint
				  AND r.slot = candidate
				  AND (r.reserved_until > now()
				       OR (r.last_used_at IS NOT NULL AND r.last_used_at > now() - $5::interval))
			)
			ORDER BY candidate
			LIMIT 1
			ON CONFLICT (base_amount, slot) DO NOTHING
			RETURNING slot`,
			baseAmount, orderID, maxOffset, ttl, cooldown,
		).Scan(&offset)

		if err == nil {
			return offset, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// Either every slot is taken, or we lost a race on the one we picked.
			// Distinguish by counting free slots; a lost race is retried.
			free, countErr := r.FreeSlots(ctx, baseAmount, maxOffset, cooldown)
			if countErr != nil {
				return 0, countErr
			}
			if free == 0 {
				return 0, domain.ErrPoolExhausted
			}
			continue
		}
		return 0, fmt.Errorf("allocator: allocate: %w", err)
	}
	return 0, fmt.Errorf("%w: lost %d allocation races", domain.ErrPoolExhausted, maxAttempts)
}

// FreeSlots counts offsets that could be allocated right now.
func (r *AllocatorRepo) FreeSlots(ctx context.Context, baseAmount int64, maxOffset int, cooldown time.Duration) (int, error) {
	var free int
	err := r.store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM generate_series(1, $2::int) AS candidate
		WHERE NOT EXISTS (
			SELECT 1 FROM amount_reservations r
			WHERE r.base_amount = $1::bigint
			  AND r.slot = candidate
			  AND (r.reserved_until > now()
			       OR (r.last_used_at IS NOT NULL AND r.last_used_at > now() - $3::interval))
		)`, baseAmount, maxOffset, cooldown).Scan(&free)
	if err != nil {
		return 0, fmt.Errorf("allocator: free slots: %w", err)
	}
	return free, nil
}

// Release frees an order's slot immediately (used on cancellation).
func (r *AllocatorRepo) Release(ctx context.Context, orderID int64) error {
	_, err := r.store.pool.Exec(ctx,
		`UPDATE amount_reservations SET reserved_until = now() WHERE order_id = $1`, orderID)
	if err != nil {
		return fmt.Errorf("allocator: release: %w", err)
	}
	return nil
}

// ReleaseBySlot frees a reservation identified by its own key, used when an
// order could not be created after the slot was already claimed.
func (r *AllocatorRepo) ReleaseBySlot(ctx context.Context, baseAmount int64, offset int) error {
	_, err := r.store.pool.Exec(ctx,
		`UPDATE amount_reservations SET reserved_until = now()
		 WHERE base_amount = $1 AND slot = $2`, baseAmount, offset)
	if err != nil {
		return fmt.Errorf("allocator: release slot: %w", err)
	}
	return nil
}

// Reassign binds a reservation to the order that finally owns it.
func (r *AllocatorRepo) Reassign(ctx context.Context, baseAmount int64, offset int, orderID int64) error {
	_, err := r.store.pool.Exec(ctx,
		`UPDATE amount_reservations SET order_id = $3
		 WHERE base_amount = $1 AND slot = $2`, baseAmount, offset, orderID)
	if err != nil {
		return fmt.Errorf("allocator: reassign: %w", err)
	}
	return nil
}

// MarkUsed starts the cooldown window after a settlement.
func (r *AllocatorRepo) MarkUsed(ctx context.Context, orderID int64) error {
	_, err := r.store.pool.Exec(ctx,
		`UPDATE amount_reservations SET last_used_at = now() WHERE order_id = $1`, orderID)
	if err != nil {
		return fmt.Errorf("allocator: mark used: %w", err)
	}
	return nil
}

// Prune deletes rows that are past both the reservation TTL and the cooldown.
func (r *AllocatorRepo) Prune(ctx context.Context, cooldown time.Duration) (int64, error) {
	tag, err := r.store.pool.Exec(ctx, `
		DELETE FROM amount_reservations
		WHERE reserved_until < now()
		  AND (last_used_at IS NULL OR last_used_at < now() - $1::interval)`, cooldown)
	if err != nil {
		return 0, fmt.Errorf("allocator: prune: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CursorRepo persists the provider feed checkpoint.
type CursorRepo struct{ store *Store }

// Cursors returns the feed cursor repository.
func (s *Store) Cursors() *CursorRepo { return &CursorRepo{store: s} }

// Load returns the stored checkpoint, or zero values when none exists.
func (r *CursorRepo) Load(ctx context.Context, provider string) (cursor string, lastTxTime time.Time, err error) {
	var c *string
	var t *time.Time
	scanErr := r.store.pool.QueryRow(ctx,
		`SELECT cursor, last_tx_time FROM feed_cursors WHERE provider = $1`, provider,
	).Scan(&c, &t)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if scanErr != nil {
		return "", time.Time{}, fmt.Errorf("cursor: load: %w", scanErr)
	}
	if c != nil {
		cursor = *c
	}
	if t != nil {
		lastTxTime = *t
	}
	return cursor, lastTxTime, nil
}

// Save writes the checkpoint after a successful poll.
func (r *CursorRepo) Save(ctx context.Context, provider, cursor string, lastTxTime time.Time) error {
	_, err := r.store.pool.Exec(ctx, `
		INSERT INTO feed_cursors (provider, cursor, last_tx_time, last_polled_at, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3, '0001-01-01'::timestamptz), now(), now())
		ON CONFLICT (provider) DO UPDATE
		SET cursor = COALESCE(EXCLUDED.cursor, feed_cursors.cursor),
		    last_tx_time = COALESCE(EXCLUDED.last_tx_time, feed_cursors.last_tx_time),
		    last_polled_at = now(),
		    updated_at = now()`,
		provider, cursor, lastTxTime)
	if err != nil {
		return fmt.Errorf("cursor: save: %w", err)
	}
	return nil
}

// SessionRepo stores encrypted provider sessions.
type SessionRepo struct{ store *Store }

// Sessions returns the provider session repository.
func (s *Store) Sessions() *SessionRepo { return &SessionRepo{store: s} }

// SessionRow is the persisted shape.
type SessionRow struct {
	Provider     string
	PayloadEnc   string
	AccountID    string
	MerchantID   string
	MerchantName string
	StoreID      string
	StoreName    string
	ExpiresAt    *time.Time
	UpdatedAt    time.Time
}

// Load returns the stored session for a provider.
func (r *SessionRepo) Load(ctx context.Context, provider string) (SessionRow, error) {
	var s SessionRow
	err := r.store.pool.QueryRow(ctx, `
		SELECT provider, payload_enc, COALESCE(account_id,''), COALESCE(merchant_id,''),
		       COALESCE(merchant_name,''), COALESCE(store_id,''), COALESCE(store_name,''),
		       expires_at, updated_at
		FROM provider_sessions WHERE provider = $1`, provider).Scan(
		&s.Provider, &s.PayloadEnc, &s.AccountID, &s.MerchantID, &s.MerchantName,
		&s.StoreID, &s.StoreName, &s.ExpiresAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	if err != nil {
		return s, fmt.Errorf("session: load: %w", err)
	}
	return s, nil
}

// Save writes (or replaces) a provider session.
func (r *SessionRepo) Save(ctx context.Context, s SessionRow) error {
	_, err := r.store.pool.Exec(ctx, `
		INSERT INTO provider_sessions (provider, payload_enc, account_id, merchant_id,
			merchant_name, store_id, store_name, expires_at, updated_at)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,now())
		ON CONFLICT (provider) DO UPDATE
		SET payload_enc = EXCLUDED.payload_enc,
		    account_id = EXCLUDED.account_id,
		    merchant_id = EXCLUDED.merchant_id,
		    merchant_name = EXCLUDED.merchant_name,
		    store_id = EXCLUDED.store_id,
		    store_name = EXCLUDED.store_name,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = now()`,
		s.Provider, s.PayloadEnc, s.AccountID, s.MerchantID, s.MerchantName,
		s.StoreID, s.StoreName, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("session: save: %w", err)
	}
	return nil
}

// Delete removes a provider session.
func (r *SessionRepo) Delete(ctx context.Context, provider string) error {
	_, err := r.store.pool.Exec(ctx, `DELETE FROM provider_sessions WHERE provider = $1`, provider)
	return err
}

// UserRepo owns the dashboard admin account.
type UserRepo struct{ store *Store }

// Users returns the admin user repository.
func (s *Store) Users() *UserRepo { return &UserRepo{store: s} }

// EnsureAdmin creates the admin user when it does not exist yet. Idempotent.
func (r *UserRepo) EnsureAdmin(ctx context.Context, username, passwordHash string) (bool, error) {
	tag, err := r.store.pool.Exec(ctx, `
		INSERT INTO admin_users (username, password_hash)
		VALUES ($1, $2)
		ON CONFLICT (username) DO NOTHING`, username, passwordHash)
	if err != nil {
		return false, fmt.Errorf("user: ensure admin: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FindByUsername returns the stored hash for a login attempt.
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (int64, string, error) {
	var id int64
	var hash string
	err := r.store.pool.QueryRow(ctx,
		`SELECT id, password_hash FROM admin_users WHERE username = $1`, username).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", domain.ErrNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("user: find: %w", err)
	}
	return id, hash, nil
}

// TouchLogin records a successful login.
func (r *UserRepo) TouchLogin(ctx context.Context, id int64) error {
	_, err := r.store.pool.Exec(ctx, `UPDATE admin_users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// Audit appends an audit entry.
func (r *UserRepo) Audit(ctx context.Context, actor, action, entity, entityID string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := r.store.pool.Exec(ctx, `
		INSERT INTO audit_log (actor, action, entity, entity_id, detail)
		VALUES ($1,$2,$3,NULLIF($4,''),$5)`, actor, action, entity, entityID, detail)
	return err
}

