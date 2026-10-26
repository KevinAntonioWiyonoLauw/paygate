package reconcile

import (
	"testing"
	"time"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
)

func ts(t time.Time) *time.Time { return &t }

func candidate(id int64, status domain.OrderStatus, expiresAt time.Time) domain.Order {
	return domain.Order{ID: id, PublicID: "ORD-TEST", Status: status, ExpiresAt: expiresAt}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	live := candidate(1, domain.OrderPending, now.Add(10*time.Minute))
	expired := candidate(2, domain.OrderPending, now.Add(-time.Minute))
	paid := candidate(3, domain.OrderPaid, now.Add(10*time.Minute))
	cancelled := candidate(4, domain.OrderCancelled, now.Add(10*time.Minute))

	settledTx := domain.Payment{
		Status:      domain.PaymentSettled,
		GrossAmount: 50001,
		TxTime:      ts(now),
	}
	settledNoTime := settledTx
	settledNoTime.TxTime = nil

	settledNoAmount := settledTx
	settledNoAmount.GrossAmount = 0
	settledNoAmount.RealAmount = 0

	settledNetOnly := settledTx
	settledNetOnly.GrossAmount = 0
	settledNetOnly.RealAmount = 50001

	capturedTx := settledTx
	capturedTx.Status = domain.PaymentCaptured

	pendingTx := settledTx
	pendingTx.Status = domain.PaymentPending

	failedTx := settledTx
	failedTx.Status = domain.PaymentFailed

	refundedTx := settledTx
	refundedTx.Status = domain.PaymentRefunded

	cases := []struct {
		name       string
		tx         domain.Payment
		candidates []domain.Order
		want       domain.MatchRule
	}{
		{
			name:       "no timestamp never settles",
			tx:         settledNoTime,
			candidates: []domain.Order{live},
			want:       domain.RuleUnmatched,
		},
		{
			name:       "no amount never settles",
			tx:         settledNoAmount,
			candidates: []domain.Order{live},
			want:       domain.RuleUnmatched,
		},
		{
			name:       "net amount is used when gross is absent",
			tx:         settledNetOnly,
			candidates: []domain.Order{live},
			want:       domain.RuleExactAmountSettled,
		},
		{
			name:       "no candidate is suspense",
			tx:         settledTx,
			candidates: nil,
			want:       domain.RuleUnmatched,
		},
		{
			name:       "settled single live candidate settles",
			tx:         settledTx,
			candidates: []domain.Order{live},
			want:       domain.RuleExactAmountSettled,
		},
		{
			name:       "capture is review even when unambiguous",
			tx:         capturedTx,
			candidates: []domain.Order{live},
			want:       domain.RuleCaptured,
		},
		{
			name:       "pending status never settles",
			tx:         pendingTx,
			candidates: []domain.Order{live},
			want:       domain.RuleUnmatched,
		},
		{
			name:       "failed status never settles",
			tx:         failedTx,
			candidates: []domain.Order{live},
			want:       domain.RuleUnmatched,
		},
		{
			name:       "refunded status never settles",
			tx:         refundedTx,
			candidates: []domain.Order{live},
			want:       domain.RuleUnmatched,
		},
		{
			name:       "two candidates is ambiguous",
			tx:         settledTx,
			candidates: []domain.Order{live, candidate(9, domain.OrderPending, now.Add(time.Hour))},
			want:       domain.RuleAmbiguous,
		},
		{
			name:       "expired candidate needs review",
			tx:         settledTx,
			candidates: []domain.Order{expired},
			want:       domain.RuleAmbiguous,
		},
		{
			name:       "already paid candidate needs review",
			tx:         settledTx,
			candidates: []domain.Order{paid},
			want:       domain.RuleAmbiguous,
		},
		{
			name:       "cancelled candidate needs review",
			tx:         settledTx,
			candidates: []domain.Order{cancelled},
			want:       domain.RuleAmbiguous,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.tx, tc.candidates, now); got != tc.want {
				t.Fatalf("Decide() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The whole point of the rule set is that money is never attributed on a guess.
// This asserts the invariant directly rather than case by case.
func TestDecideNeverSettlesOnWeakEvidence(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	live := candidate(1, domain.OrderPending, now.Add(time.Hour))

	weak := []domain.Payment{
		{Status: domain.PaymentSettled, GrossAmount: 50001},                       // no time
		{Status: domain.PaymentSettled, TxTime: ts(now)},                          // no amount
		{Status: domain.PaymentPending, GrossAmount: 50001, TxTime: ts(now)},      // not cleared
		{Status: domain.PaymentCaptured, GrossAmount: 50001, TxTime: ts(now)},     // held
		{Status: domain.PaymentFailed, GrossAmount: 50001, TxTime: ts(now)},       // failed
		{Status: domain.PaymentRefunded, GrossAmount: 50001, TxTime: ts(now)},     // reversed
		{Status: domain.PaymentStatus(""), GrossAmount: 50001, TxTime: ts(now)},   // unknown
	}

	for _, tx := range weak {
		if got := Decide(tx, []domain.Order{live}, now); got == domain.RuleExactAmountSettled {
			t.Fatalf("status %q settled an order on weak evidence", tx.Status)
		}
	}

	// Ambiguity is also a refusal to settle.
	two := []domain.Order{live, candidate(2, domain.OrderPending, now.Add(time.Hour))}
	settled := domain.Payment{Status: domain.PaymentSettled, GrossAmount: 50001, TxTime: ts(now)}
	if got := Decide(settled, two, now); got == domain.RuleExactAmountSettled {
		t.Fatal("two candidates settled an order on a coin flip")
	}
}
