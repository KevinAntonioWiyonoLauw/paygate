// Package domain holds the gateway's core types, state machine, and errors.
// It has no infrastructure dependencies so the rules stay testable in isolation.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OrderStatus is the lifecycle of an order.
type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderPaid      OrderStatus = "paid"
	OrderReview    OrderStatus = "review"
	OrderExpired   OrderStatus = "expired"
	OrderCancelled OrderStatus = "cancelled"
)

// PaymentStatus is the lifecycle of a provider transaction.
type PaymentStatus string

const (
	PaymentPending  PaymentStatus = "pending"
	PaymentCaptured PaymentStatus = "captured"
	PaymentSettled  PaymentStatus = "settled"
	PaymentFailed   PaymentStatus = "failed"
	PaymentRefunded PaymentStatus = "refunded"
)

// Order is a payment request issued to a buyer.
type Order struct {
	ID           int64             `json:"-"`
	PublicID     string            `json:"public_id"`
	BaseAmount   int64             `json:"base_amount"`
	UniqueOffset int               `json:"unique_offset"`
	TotalAmount  int64             `json:"total_amount"`
	Status       OrderStatus       `json:"status"`
	Source       string            `json:"source"`
	Provider     string            `json:"provider"`
	QRPayload    string            `json:"qr_payload"`
	Reference    string            `json:"reference,omitempty"`
	Description  string            `json:"description,omitempty"`
	Metadata     map[string]any    `json:"metadata"`
	ExpiresAt    time.Time         `json:"expires_at"`
	PaidAt       *time.Time        `json:"paid_at,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// Payment is one provider transaction observed in the feed.
type Payment struct {
	// ID is exposed because a payment has no public handle: the manual match
	// endpoint is addressed by this primary key.
	ID           int64         `json:"id"`
	Provider     string        `json:"provider"`
	ProviderTxID string        `json:"provider_tx_id"`
	GrossAmount  int64         `json:"gross_amount"`
	RealAmount   int64         `json:"real_amount,omitempty"`
	Status       PaymentStatus `json:"status"`
	TxTime       *time.Time    `json:"tx_time,omitempty"`
	OrderID      *int64        `json:"-"`
	OrderPublicID string       `json:"order_public_id,omitempty"`
	MatchRule    *int          `json:"match_rule,omitempty"`
	Raw          []byte        `json:"-"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Matched reports whether this payment has been attributed to an order.
func (p Payment) Matched() bool { return p.OrderID != nil }

// Terminal reports whether the order can no longer change without manual action.
func (o Order) Terminal() bool {
	switch o.Status {
	case OrderPaid, OrderExpired, OrderCancelled:
		return true
	}
	return false
}

// Payable reports whether the order still accepts money.
func (o Order) Payable(now time.Time) bool {
	return o.Status == OrderPending && now.Before(o.ExpiresAt)
}

// MatchRule identifies which reconciliation rule produced a match. Recorded so a
// settlement can always be explained after the fact.
type MatchRule int

const (
	// RuleExactAmountSettled: settled status, exact amount, inside the window.
	RuleExactAmountSettled MatchRule = 1
	// RuleCaptured: funds held but not settled. Needs a human.
	RuleCaptured MatchRule = 2
	// RuleAmbiguous: amount matches more than one candidate, or the time is
	// outside the window. Needs a human.
	RuleAmbiguous MatchRule = 3
	// RuleUnmatched: nothing matched at all. Suspense.
	RuleUnmatched MatchRule = 4
	// RuleManual: attributed by an operator.
	RuleManual MatchRule = 5
)

func (r MatchRule) String() string {
	switch r {
	case RuleExactAmountSettled:
		return "exact_amount_settled"
	case RuleCaptured:
		return "captured"
	case RuleAmbiguous:
		return "ambiguous"
	case RuleUnmatched:
		return "unmatched"
	case RuleManual:
		return "manual"
	}
	return "unknown"
}

// ---- Errors ----

var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrPoolExhausted = errors.New("amount offset pool exhausted")
	ErrInvalidState  = errors.New("invalid state transition")
)

// NewPublicID builds a human-readable, sortable order id: ORD-20261026-4F2A9C.
// The random suffix keeps it unguessable enough that ids cannot be enumerated.
func NewPublicID(now time.Time) (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate order id: %w", err)
	}
	suffix := strings.ToUpper(hex.EncodeToString(buf[:]))
	return fmt.Sprintf("ORD-%s-%s", now.Format("20060102"), suffix), nil
}

// ValidateTransition reports an error when moving from -> to is not allowed.
// Keeping the legal edges in one place means the API layer cannot invent a
// transition that skips settlement.
func ValidateTransition(from, to OrderStatus) error {
	allowed := map[OrderStatus][]OrderStatus{
		OrderPending:   {OrderPaid, OrderReview, OrderExpired, OrderCancelled},
		OrderReview:    {OrderPaid, OrderCancelled},
		OrderPaid:      {},
		OrderExpired:   {OrderPaid}, // a late payment can still settle an expired order
		OrderCancelled: {OrderPaid}, // same, money beats bookkeeping
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidState, from, to)
}
