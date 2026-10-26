// Package provider adapts the upstream paygateme Shopee SDK to the gateway's
// own domain. The adapter owns three responsibilities the SDK does not:
//
//  1. Feed polling with a durable cursor, so a restart neither rescans nor skips.
//  2. Session persistence, encrypted at rest.
//  3. Normalisation into domain.Payment, including the capture/settled split the
//     upstream matcher collapses.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hirotomasato/paygateme/core"
	"github.com/hirotomasato/paygateme/shopee"
	"github.com/hirotomasato/paygateme/utils"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
)

// ProviderID is the canonical identifier.
const ProviderID = "shopee"

// settledStatuses are statuses that mean money actually reached the merchant.
// `capture` is deliberately NOT here: it is an authorisation, not a settlement.
var settledStatuses = map[string]bool{
	"settlement": true,
	"settled":    true,
	"success":    true,
	"paid":       true,
	"completed":  true,
}

// capturedStatuses are statuses that mean funds are held but not released.
var capturedStatuses = map[string]bool{
	"capture":   true,
	"authorize": true,
	"authorized": true,
}

// FeedResult is one polling pass.
type FeedResult struct {
	Transactions []domain.Payment
	Cursor       string
	LastTxTime   time.Time
	Truncated    bool
	PagesFetched int
}

// Adapter wraps a logged-in Shopee session and exposes feed polling.
type Adapter struct {
	http       *shopee.HTTPClient
	feed       *shopee.TransactionFeed
	session    *shopee.Session
	merchantID string
	storeID    string

	mu sync.RWMutex
}

// NewAdapter builds an adapter from a restored session. The session must carry
// a merchant token in its cookies; that is what the SDK reads internally.
func NewAdapter(session *shopee.Session) (*Adapter, error) {
	if session == nil {
		return nil, fmt.Errorf("provider: nil session")
	}
	if session.StoreID == "" {
		return nil, fmt.Errorf("provider: session has no store id")
	}
	httpClient := shopee.NewHTTPClient(utils.NoopLogger)
	httpClient.Jar().Restore(session.Cookies)

	credential, err := shopee.ReadMerchantCredential(httpClient.Jar())
	if err != nil {
		return nil, fmt.Errorf("provider: session has no live merchant token: %w", err)
	}

	feed := shopee.NewTransactionFeed(
		httpClient, credential.Token, session.Merchant.ID, session.StoreID,
		shopee.APILocale{}, utils.NoopLogger,
	)
	return &Adapter{
		http:       httpClient,
		feed:       feed,
		session:    session,
		merchantID: session.Merchant.ID,
		storeID:    session.StoreID,
	}, nil
}

// MerchantName returns the merchant display name from the session.
func (a *Adapter) MerchantName() string { return a.session.Merchant.Name }

// MerchantID returns the Shopee business merchant id.
func (a *Adapter) MerchantID() string { return a.merchantID }

// StoreID returns the selected store id.
func (a *Adapter) StoreID() string { return a.storeID }

// Scope returns the payment scope for this session.
func (a *Adapter) Scope() core.PaymentScope {
	return core.PaymentScope{
		Provider:   ProviderID,
		AccountID:  a.merchantID,
		MerchantID: a.storeID,
	}
}

// Session returns the underlying session (for persistence after a refresh).
func (a *Adapter) Session() *shopee.Session { return a.session }

// ReplaceSession swaps in a refreshed session.
func (a *Adapter) ReplaceSession(session *shopee.Session) error {
	refreshed, err := NewAdapter(session)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.http = refreshed.http
	a.feed = refreshed.feed
	a.session = refreshed.session
	a.merchantID = refreshed.merchantID
	a.storeID = refreshed.storeID
	a.mu.Unlock()
	return nil
}

// FetchWindow polls the feed for transactions inside [from, to].
//
// The upstream TransactionFeed owns pagination and returns whole-rupiah amounts
// already normalised, so this method's job is status mapping and cursor capture.
func (a *Adapter) FetchWindow(ctx context.Context, from, to time.Time) (FeedResult, error) {
	a.mu.RLock()
	feed := a.feed
	a.mu.RUnlock()

	result, err := feed.ListRecent(ctx, core.TransactionFeedQuery{
		Scope:     a.Scope(),
		StartTime: from,
		EndTime:   to,
		PageSize:  core.DefaultTransactionPageSize,
		MaxPages:  core.MaxTransactionPagesPerTick,
	})
	if err != nil {
		return FeedResult{}, fmt.Errorf("provider: fetch feed: %w", err)
	}

	out := FeedResult{
		Transactions: make([]domain.Payment, 0, len(result.Transactions)),
		Truncated:    result.Truncated,
		PagesFetched: result.PagesFetched,
	}
	for _, tx := range result.Transactions {
		if tx.ID == "" {
			continue
		}
		converted := convertTransaction(tx)
		if converted.TxTime != nil && converted.TxTime.After(out.LastTxTime) {
			out.LastTxTime = *converted.TxTime
		}
		out.Transactions = append(out.Transactions, converted)
	}
	return out, nil
}

// convertTransaction maps a provider transaction to the domain model.
//
// Status mapping is fail-closed on purpose: an unknown or empty status becomes
// `pending`, never `settled`. The upstream matcher treats empty as success,
// which can settle an order on a transaction that has not actually cleared.
func convertTransaction(tx core.MerchantTransaction) domain.Payment {
	status := normalizeStatus(tx.Status)

	var txTime *time.Time
	if !tx.Time.IsZero() {
		utc := tx.Time.UTC()
		txTime = &utc
	}

	raw, err := json.Marshal(tx)
	if err != nil {
		raw = []byte("{}")
	}

	return domain.Payment{
		Provider:     ProviderID,
		ProviderTxID: tx.ID,
		GrossAmount:  tx.Amount,
		RealAmount:   tx.RealAmount,
		Status:       status,
		TxTime:       txTime,
		Raw:          raw,
	}
}

// normalizeStatus is the single place that decides what counts as money.
func normalizeStatus(raw string) domain.PaymentStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case settledStatuses[normalized]:
		return domain.PaymentSettled
	case capturedStatuses[normalized]:
		return domain.PaymentCaptured
	case normalized == "failed", normalized == "fail", normalized == "cancelled", normalized == "canceled":
		return domain.PaymentFailed
	case normalized == "refund", normalized == "refunded":
		return domain.PaymentRefunded
	default:
		// Unknown or empty: not money yet. This is the fail-closed branch.
		return domain.PaymentPending
	}
}

// LoginRequest is the input to a manual OTP login.
type LoginRequest struct {
	Phone        string
	Password     string
	DeviceReport string
}

// LoginChallenge is the state a caller must echo back with the OTP.
type LoginChallenge struct {
	Challenge  shopee.OtpChallenge
	Merchants  []shopee.MerchantSummary
	NeedsMerchant bool
}

// LoginClient drives the OTP flow against Shopee.
type LoginClient struct {
	provider *shopee.Provider
}

// NewLoginClient builds a login client. deviceReport should be captured from a
// real browser session; an empty value falls back to the upstream shared blob,
// which links every install to the same device fingerprint.
func NewLoginClient(deviceReport string) *LoginClient {
	return &LoginClient{
		provider: shopee.NewProvider(shopee.ProviderConfig{
			DeviceReport: deviceReport,
			Logger:       utils.NoopLogger,
		}),
	}
}

// RequestOtp sends an OTP and returns the challenge to present to the operator.
func (c *LoginClient) RequestOtp(ctx context.Context, req LoginRequest) (*LoginChallenge, error) {
	challenge, err := c.provider.RequestOtp(ctx, req.Phone, shopee.OtpRequestOptions{
		Password:     req.Password,
		DeviceReport: req.DeviceReport,
	})
	if err != nil {
		return nil, fmt.Errorf("provider: request otp: %w", err)
	}
	return &LoginChallenge{Challenge: *challenge}, nil
}

// VerifyOtp completes the login and returns a session ready to persist.
func (c *LoginClient) VerifyOtp(ctx context.Context, challenge shopee.OtpChallenge, otp, merchantID, storeID string) (*shopee.Session, []shopee.MerchantSummary, error) {
	outcome, err := c.provider.LoginWithOtp(ctx, shopee.LoginWithOtpInput{
		Challenge:  challenge,
		OTP:        otp,
		MerchantID: merchantID,
		StoreID:    storeID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("provider: verify otp: %w", err)
	}
	if outcome.Status != shopee.LoginComplete {
		// Multiple merchants: the caller must pick one and call again.
		return nil, outcome.Merchants, nil
	}
	return outcome.Session, outcome.Merchants, nil
}

// CompleteLogin finishes a login for a chosen merchant.
func (c *LoginClient) CompleteLogin(ctx context.Context, challenge shopee.OtpChallenge, otp, merchantID, storeID string) (*shopee.Session, error) {
	verification, err := c.provider.VerifyOtp(ctx, shopee.VerifyOtpInput{
		Challenge: challenge,
		OTP:       otp,
	})
	if err != nil {
		return nil, fmt.Errorf("provider: verify otp: %w", err)
	}
	session, err := c.provider.CompleteLogin(ctx, shopee.CompleteLoginInput{
		Verification: *verification,
		MerchantID:   merchantID,
		StoreID:      storeID,
	})
	if err != nil {
		return nil, fmt.Errorf("provider: complete login: %w", err)
	}
	return session, nil
}
