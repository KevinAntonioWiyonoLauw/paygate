// Package provider — OTP login state, session encryption, and persistence.
package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hirotomasato/paygateme/shopee"

	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

// LoginService implements api.LoginService: it drives the OTP flow, holds the
// pending challenge in memory (it is short-lived by nature), and persists the
// resulting session encrypted at rest.
type LoginService struct {
	store     *postgres.Store
	aead      cipher.AEAD
	device    string
	client    *LoginClient

	mu      sync.Mutex
	pending *pendingLogin
}

type pendingLogin struct {
	challenge shopee.OtpChallenge
	createdAt time.Time
}

// LoginOptions configures the login service.
type LoginOptions struct {
	Store        *postgres.Store
	EncryptionKey []byte
	DeviceReport string
}

// NewLoginService builds the login service. EncryptionKey must be 32 bytes.
func NewLoginService(opts LoginOptions) (*LoginService, error) {
	block, err := aes.NewCipher(opts.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("provider: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("provider: init gcm: %w", err)
	}
	return &LoginService{
		store:  opts.Store,
		aead:   aead,
		device: opts.DeviceReport,
		client: NewLoginClient(opts.DeviceReport),
	}, nil
}

// RequestOTP sends an OTP and stashes the challenge for VerifyOTP.
func (l *LoginService) RequestOTP(ctx context.Context, phone, password, deviceReport string) (*shopee.OtpChallenge, error) {
	if deviceReport == "" {
		deviceReport = l.device
	}
	challenge, err := l.client.RequestOtp(ctx, LoginRequest{
		Phone:        phone,
		Password:     password,
		DeviceReport: deviceReport,
	})
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.pending = &pendingLogin{challenge: challenge.Challenge, createdAt: time.Now()}
	l.mu.Unlock()
	return &challenge.Challenge, nil
}

// VerifyOTP completes the login. Returns (nil, merchants, nil) when the account
// has more than one merchant and the caller must choose.
func (l *LoginService) VerifyOTP(ctx context.Context, otp, merchantID, storeID string) (*shopee.Session, []shopee.MerchantSummary, error) {
	l.mu.Lock()
	pending := l.pending
	l.mu.Unlock()

	if pending == nil {
		return nil, nil, fmt.Errorf("provider: no OTP was requested, or it expired")
	}
	if time.Since(pending.createdAt) > 10*time.Minute {
		l.mu.Lock()
		l.pending = nil
		l.mu.Unlock()
		return nil, nil, fmt.Errorf("provider: OTP challenge expired, request a new one")
	}

	session, merchants, err := l.client.VerifyOtp(ctx, pending.challenge, otp, merchantID, storeID)
	if err != nil {
		return nil, nil, err
	}
	if session != nil {
		l.mu.Lock()
		l.pending = nil
		l.mu.Unlock()
	}
	return session, merchants, nil
}

// PersistSession encrypts and stores a session.
func (l *LoginService) PersistSession(ctx context.Context, session *shopee.Session) error {
	if session == nil {
		return fmt.Errorf("provider: nil session")
	}
	plain, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("provider: marshal session: %w", err)
	}
	sealed, err := l.seal(plain)
	if err != nil {
		return err
	}

	row := postgres.SessionRow{
		Provider:     ProviderID,
		PayloadEnc:   sealed,
		AccountID:    session.AccountID,
		MerchantID:   session.Merchant.ID,
		MerchantName: session.Merchant.Name,
		StoreID:      session.StoreID,
	}
	if session.ExpiresAt > 0 {
		expiry := time.UnixMilli(session.ExpiresAt).UTC()
		row.ExpiresAt = &expiry
	}
	return l.store.Sessions().Save(ctx, row)
}

// LoadSession decrypts the stored session, if any.
func (l *LoginService) LoadSession(ctx context.Context) (*shopee.Session, error) {
	row, err := l.store.Sessions().Load(ctx, ProviderID)
	if err != nil {
		return nil, err
	}
	plain, err := l.open(row.PayloadEnc)
	if err != nil {
		return nil, err
	}
	var session shopee.Session
	if err := json.Unmarshal(plain, &session); err != nil {
		return nil, fmt.Errorf("provider: unmarshal session: %w", err)
	}
	return &session, nil
}

// seal encrypts with AES-256-GCM and returns base64(nonce||ciphertext).
func (l *LoginService) seal(plain []byte) (string, error) {
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("provider: nonce: %w", err)
	}
	out := l.aead.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// open reverses seal.
func (l *LoginService) open(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("provider: decode session: %w", err)
	}
	if len(raw) < l.aead.NonceSize() {
		return nil, fmt.Errorf("provider: session ciphertext is truncated")
	}
	nonce, ciphertext := raw[:l.aead.NonceSize()], raw[l.aead.NonceSize():]
	plain, err := l.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("provider: decrypt session: %w", err)
	}
	return plain, nil
}
