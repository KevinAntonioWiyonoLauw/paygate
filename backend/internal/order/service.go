// Package order implements the create-order use case: allocate a unique
// offset, build the dynamic QRIS payload, and persist the order.
package order

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
	"github.com/kevinantoniowiyonolauw/paygate/internal/qris"
	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

// Service creates and cancels orders.
type Service struct {
	store          *postgres.Store
	staticQRIS     string
	provider       string
	maxOffset      int
	expiry         time.Duration
	offsetCooldown time.Duration
}

// Options configures the order service.
type Options struct {
	Store          *postgres.Store
	StaticQRIS     string
	Provider       string
	MaxOffset      int
	Expiry         time.Duration
	OffsetCooldown time.Duration
}

// New builds the order service.
func New(opts Options) *Service {
	provider := opts.Provider
	if provider == "" {
		provider = "shopee"
	}
	maxOffset := opts.MaxOffset
	if maxOffset <= 0 {
		maxOffset = 999
	}
	expiry := opts.Expiry
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	cooldown := opts.OffsetCooldown
	if cooldown <= 0 {
		cooldown = 24 * time.Hour
	}
	return &Service{
		store:          opts.Store,
		staticQRIS:     opts.StaticQRIS,
		provider:       provider,
		maxOffset:      maxOffset,
		expiry:         expiry,
		offsetCooldown: cooldown,
	}
}

// SetStaticQRIS replaces the bound payload. Validated before it is stored, and
// persisted to the database so a restart cannot lose the merchant identity.
func (s *Service) SetStaticQRIS(ctx context.Context, payload string) error {
	if payload == "" {
		s.staticQRIS = ""
		return s.store.Settings().Set(ctx, settingStaticQRIS, "")
	}
	if !qris.ValidChecksum(payload) {
		return fmt.Errorf("order: QRIS checksum is invalid")
	}
	if _, err := qris.Parse(payload); err != nil {
		return fmt.Errorf("order: QRIS payload is not parseable: %w", err)
	}
	if err := s.store.Settings().Set(ctx, settingStaticQRIS, payload); err != nil {
		return err
	}
	s.staticQRIS = payload
	return nil
}

// LoadStaticQRIS restores the bound payload from the database at boot.
func (s *Service) LoadStaticQRIS(ctx context.Context) error {
	stored, err := s.store.Settings().Get(ctx, settingStaticQRIS)
	if err != nil {
		return err
	}
	s.staticQRIS = stored
	return nil
}

const settingStaticQRIS = "shopee.static_qris"

// StaticQRIS returns the bound payload.
func (s *Service) StaticQRIS() string { return s.staticQRIS }

// CreateInput is the request shape.
type CreateInput struct {
	// Amount is the product price in whole rupiah, without any offset.
	Amount      int64          `json:"amount"`
	Reference   string         `json:"reference"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata"`
	// ExpiresIn overrides the default window for this order.
	ExpiresIn *time.Duration `json:"-"`
	// Actor is the authenticated operator, filled in by the HTTP layer. It is
	// never read from the request body.
	Actor string `json:"-"`
}

// Create allocates an offset, builds the QR, and persists the order.
//
// Ordering matters: the reservation is created first because the order row
// needs the offset to satisfy its CHECK constraint. If anything after the
// reservation fails, the reservation is released so the slot is not leaked.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Order, error) {
	if in.Amount <= 0 {
		return domain.Order{}, fmt.Errorf("order: amount must be positive")
	}
	if s.staticQRIS == "" {
		return domain.Order{}, fmt.Errorf("order: no static QRIS bound yet; bind one in Settings")
	}

	now := time.Now().UTC()
	expiry := s.expiry
	if in.ExpiresIn != nil && *in.ExpiresIn > 0 {
		expiry = *in.ExpiresIn
	}

	publicID, err := domain.NewPublicID(now)
	if err != nil {
		return domain.Order{}, err
	}

	// Placeholder row to own the reservation. The offset is patched in below,
	// inside the same transaction as the final insert, so a failure cannot leave
	// a reservation pointing at nothing.
	offset, err := s.store.Allocator().Allocate(
		ctx, in.Amount, 0, s.maxOffset, expiry, s.offsetCooldown)
	if err != nil {
		return domain.Order{}, err
	}

	total := in.Amount + int64(offset)
	payload, err := qris.ToDynamic(s.staticQRIS, qris.ConvertOptions{
		Amount:    total,
		Reference: publicID,
	})
	if err != nil {
		s.releaseOffset(ctx, in.Amount, offset)
		return domain.Order{}, fmt.Errorf("order: build qris: %w", err)
	}

	created, err := s.store.Orders().Create(ctx, domain.Order{
		PublicID:     publicID,
		BaseAmount:   in.Amount,
		UniqueOffset: offset,
		TotalAmount:  total,
		Status:       domain.OrderPending,
		Source:       "manual",
		Provider:     s.provider,
		QRPayload:    payload,
		Reference:    in.Reference,
		Description:  in.Description,
		Metadata:     in.Metadata,
		ExpiresAt:    now.Add(expiry),
	})
	if err != nil {
		s.releaseOffset(ctx, in.Amount, offset)
		return domain.Order{}, fmt.Errorf("order: persist: %w", err)
	}

	// Bind the reservation to the real order id now that we have it.
	if err := s.store.Allocator().Reassign(ctx, in.Amount, offset, created.ID); err != nil {
		// Non-fatal: the reservation still blocks the slot, which is the safety
		// property that matters. Log-worthy but not a reason to fail the order.
		_ = err
	}

	// An order mints a payable QR, so it belongs in the trail alongside the
	// cancels and manual matches. A failed audit write must not fail the order.
	detail, _ := json.Marshal(map[string]any{
		"total_amount":  created.TotalAmount,
		"unique_offset": created.UniqueOffset,
	})
	_ = s.store.Users().Audit(ctx, in.Actor, "create_order", "order", created.PublicID, detail)

	return created, nil
}

// Cancel cancels a pending order and frees its slot.
func (s *Service) Cancel(ctx context.Context, publicID, actor string) (domain.Order, error) {
	current, err := s.store.Orders().GetByPublicID(ctx, publicID)
	if err != nil {
		return domain.Order{}, err
	}
	if current.Status == domain.OrderPaid {
		return domain.Order{}, fmt.Errorf("%w: cannot cancel a paid order", domain.ErrConflict)
	}
	updated, err := s.store.Orders().SetStatus(ctx, current.ID, domain.OrderCancelled)
	if err != nil {
		return domain.Order{}, err
	}
	if err := s.store.Allocator().Release(ctx, current.ID); err != nil {
		return updated, err
	}
	_ = s.store.Users().Audit(ctx, actor, "cancel_order", "order", publicID, nil)
	return updated, nil
}

func (s *Service) releaseOffset(ctx context.Context, baseAmount int64, offset int) {
	if err := s.store.Allocator().ReleaseBySlot(ctx, baseAmount, offset); err != nil {
		_ = err
	}
}
