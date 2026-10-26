// Package reconcile turns a stream of provider transactions into order state.
//
// Three rules keep money safe, and each one is enforced here rather than
// delegated to the provider SDK:
//
//  1. Fail closed. A transaction whose status we cannot classify never settles
//     an order. Unknown becomes pending, not paid.
//  2. Capture is not settlement. Funds held are routed to `review`, not `paid`.
//  3. Amount is not proof. A match also needs a time window, and an ambiguous
//     match (two candidates) goes to review instead of picking one.
package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
	"github.com/kevinantoniowiyonolauw/paygate/internal/provider"
	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

// Options configures the worker.
type Options struct {
	Store        *postgres.Store
	Provider     *provider.Adapter
	Logger       *slog.Logger
	PollInterval time.Duration
	// Lookback is how far back each poll scans. A rolling window avoids the
	// day-boundary gap a calendar-day query would create between UTC and WIB.
	Lookback   time.Duration
	ClockSkew  time.Duration
	ExpiryTick time.Duration
}

// Worker polls the feed and settles orders.
type Worker struct {
	store        *postgres.Store
	provider     *provider.Adapter
	log          *slog.Logger
	pollInterval time.Duration
	lookback     time.Duration
	clockSkew    time.Duration
	expiryTick   time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New builds a worker.
func New(opts Options) *Worker {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	lookback := opts.Lookback
	if lookback <= 0 {
		lookback = 6 * time.Hour
	}
	clockSkew := opts.ClockSkew
	if clockSkew <= 0 {
		clockSkew = 90 * time.Second
	}
	expiryTick := opts.ExpiryTick
	if expiryTick <= 0 {
		expiryTick = 15 * time.Second
	}
	return &Worker{
		store:        opts.Store,
		provider:     opts.Provider,
		log:          log,
		pollInterval: pollInterval,
		lookback:     lookback,
		clockSkew:    clockSkew,
		expiryTick:   expiryTick,
		stopCh:       make(chan struct{}),
	}
}

// Start launches the polling and expiry loops.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.pollLoop(ctx)
	}()
	w.StartExpiry(ctx)
}

// StartExpiry launches only the local housekeeping loop.
//
// Expiring orders and pruning reservations are local database operations: they
// need no provider session. Keeping them behind the polling worker would leave
// abandoned orders pending forever whenever Shopee is not logged in, which is
// exactly when nobody is watching.
func (w *Worker) StartExpiry(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.expiryLoop(ctx)
	}()
}

// Stop signals both loops and waits for them to finish.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *Worker) pollLoop(ctx context.Context) {
	// First pass immediately, so the dashboard has data at boot.
	w.tick(ctx)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) expiryLoop(ctx context.Context) {
	ticker := time.NewTicker(w.expiryTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			if _, err := w.store.Orders().ExpireStale(ctx, 200); err != nil {
				w.log.Error("expire stale orders", "error", err)
			}
			if _, err := w.store.Allocator().Prune(ctx, 24*time.Hour); err != nil {
				w.log.Error("prune reservations", "error", err)
			}
		}
	}
}

// TickResult summarises one reconciliation pass.
type TickResult struct {
	Fetched int `json:"fetched"`
	New     int `json:"new"`
	Settled int `json:"settled"`
	Review  int `json:"review"`
	Suspense int `json:"suspense"`
}

func (w *Worker) tick(ctx context.Context) {
	result, err := w.Tick(ctx)
	if err != nil {
		w.log.Error("reconcile tick", "error", err)
		return
	}
	if result.New > 0 || result.Settled > 0 || result.Suspense > 0 {
		w.log.Info("reconcile",
			"fetched", result.Fetched, "new", result.New,
			"settled", result.Settled, "review", result.Review, "suspense", result.Suspense)
	}
}

// Tick runs one reconciliation pass. Exported so the API can force a refresh.
func (w *Worker) Tick(ctx context.Context) (TickResult, error) {
	var result TickResult

	now := time.Now().UTC()
	// Start from the stored checkpoint when it is newer than the lookback floor,
	// so a restart resumes instead of rescanning the whole window.
	_, lastTxTime, err := w.store.Cursors().Load(ctx, provider.ProviderID)
	if err != nil {
		return result, fmt.Errorf("reconcile: load cursor: %w", err)
	}
	from := now.Add(-w.lookback)
	if !lastTxTime.IsZero() && lastTxTime.After(from) {
		from = lastTxTime.Add(-w.clockSkew)
	}

	feed, err := w.provider.FetchWindow(ctx, from, now)
	if err != nil {
		return result, err
	}
	result.Fetched = len(feed.Transactions)

	for _, tx := range feed.Transactions {
		created, err := w.ingest(ctx, tx)
		if err != nil {
			w.log.Error("ingest transaction", "tx", tx.ProviderTxID, "error", err)
			continue
		}
		if created {
			result.New++
		}
		switch tx.Status {
		case domain.PaymentSettled:
			result.Settled++
		case domain.PaymentCaptured:
			result.Review++
		}
	}

	if err := w.store.Cursors().Save(ctx, provider.ProviderID, feed.Cursor, feed.LastTxTime); err != nil {
		return result, fmt.Errorf("reconcile: save cursor: %w", err)
	}
	return result, nil
}

// ingest stores one transaction and attempts to settle an order.
//
// The idempotency guarantee lives in the database: payments has a unique
// (provider, provider_tx_id). A re-delivered transaction returns created=false
// and is skipped, so a restart mid-tick cannot double-settle.
func (w *Worker) ingest(ctx context.Context, tx domain.Payment) (bool, error) {
	stored, created, err := w.store.Payments().Upsert(ctx, tx)
	if err != nil {
		return false, err
	}
	if !created {
		// Already known. If it was settled but never attached, try once more.
		if stored.OrderID != nil || stored.Status != domain.PaymentSettled {
			return false, nil
		}
	}
	if stored.Status == domain.PaymentFailed || stored.Status == domain.PaymentRefunded {
		return created, nil
	}

	rule, orderID, err := w.match(ctx, stored)
	if err != nil {
		return created, err
	}
	if orderID == nil {
		// Suspense. The row stays with order_id NULL for a human to resolve.
		return created, nil
	}

	settle := rule == domain.RuleExactAmountSettled
	if _, err := w.store.Orders().AttachPayment(ctx, *orderID, stored.ID, rule, settle); err != nil {
		return created, fmt.Errorf("reconcile: attach payment %d to order %d: %w", stored.ID, *orderID, err)
	}
	return created, nil
}

// match decides which order a transaction settles, applying the rules in order.
func (w *Worker) match(ctx context.Context, tx domain.Payment) (domain.MatchRule, *int64, error) {
	if tx.TxTime == nil {
		// Without a timestamp we cannot prove the money belongs to a window.
		return domain.RuleUnmatched, nil, nil
	}
	txTime := *tx.TxTime
	from := txTime.Add(-w.clockSkew)
	to := txTime.Add(w.clockSkew)

	amount := tx.GrossAmount
	if amount <= 0 && tx.RealAmount > 0 {
		amount = tx.RealAmount
	}
	if amount <= 0 {
		return domain.RuleUnmatched, nil, nil
	}

	candidates, err := w.store.Orders().PendingCandidates(ctx, amount, from, to)
	if err != nil {
		return domain.RuleUnmatched, nil, err
	}

	rule := Decide(tx, candidates, time.Now().UTC())
	switch rule {
	case domain.RuleExactAmountSettled, domain.RuleCaptured, domain.RuleAmbiguous:
		// These all point at a specific candidate to record on the payment row.
		// Only RuleExactAmountSettled also settles it; the rest await a human.
		return rule, &candidates[0].ID, nil
	default:
		return rule, nil, nil
	}
}

// Decide applies the matching rules to an already-fetched candidate set.
//
// It is a pure function on purpose: the rules decide whether money settles an
// order, so they are exercised directly by tests rather than only through a
// live provider feed that may not be reachable.
//
// Rules, in order:
//  1. No timestamp or no amount: unmatched. Both are needed to prove the money
//     belongs to a particular order.
//  2. No candidates: unmatched, which becomes a suspense row.
//  3. Capture: funds are held, not received. Never auto-settles, even when the
//     candidate is unambiguous.
//  4. Not settled: anything else (pending, failed, refunded) never settles.
//  5. More than one candidate: ambiguous. Picking one would be a coin flip with
//     someone's money, so a human decides.
//  6. A single candidate that is no longer payable (expired/cancelled): a late
//     payment must be reviewed, not auto-settled.
//  7. Otherwise: settled, exact amount, inside the window.
func Decide(tx domain.Payment, candidates []domain.Order, now time.Time) domain.MatchRule {
	if tx.TxTime == nil {
		return domain.RuleUnmatched
	}
	amount := tx.GrossAmount
	if amount <= 0 && tx.RealAmount > 0 {
		amount = tx.RealAmount
	}
	if amount <= 0 {
		return domain.RuleUnmatched
	}
	if len(candidates) == 0 {
		return domain.RuleUnmatched
	}
	if tx.Status == domain.PaymentCaptured {
		return domain.RuleCaptured
	}
	if tx.Status != domain.PaymentSettled {
		return domain.RuleUnmatched
	}
	if len(candidates) > 1 {
		return domain.RuleAmbiguous
	}
	if candidates[0].Status != domain.OrderPending {
		return domain.RuleAmbiguous
	}
	if !candidates[0].Payable(now) {
		return domain.RuleAmbiguous
	}
	return domain.RuleExactAmountSettled
}
