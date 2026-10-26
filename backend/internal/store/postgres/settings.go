package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SettingsRepo is a small key/value store for runtime configuration that must
// outlive the process.
type SettingsRepo struct{ store *Store }

// Settings returns the settings repository.
func (s *Store) Settings() *SettingsRepo { return &SettingsRepo{store: s} }

// Get returns the value, or "" when the key is absent.
func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.store.pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: get %s: %w", key, err)
	}
	return value, nil
}

// Set upserts a value.
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.store.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	if err != nil {
		return fmt.Errorf("settings: set %s: %w", key, err)
	}
	return nil
}
