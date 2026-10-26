package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

// Auth handles the single-admin login flow: bcrypt password, signed cookie.
type Auth struct {
	store        *postgres.Store
	secret       []byte
	cookieName   string
	tokenTTL     time.Duration
	secureCookie bool
}

// AuthOptions configures Auth.
type AuthOptions struct {
	Store        *postgres.Store
	Secret       []byte
	CookieName   string
	TokenTTL     time.Duration
	SecureCookie bool
}

// NewAuth builds the auth handler.
func NewAuth(opts AuthOptions) *Auth {
	name := opts.CookieName
	if name == "" {
		name = "paygate_session"
	}
	ttl := opts.TokenTTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Auth{
		store:        opts.Store,
		secret:       opts.Secret,
		cookieName:   name,
		tokenTTL:     ttl,
		secureCookie: opts.SecureCookie,
	}
}

type sessionClaims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"usr"`
	jwt.RegisteredClaims
}

// Login validates credentials and sets a signed session cookie.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "username and password are required")
		return
	}

	id, hash, err := a.store.Users().FindByUsername(r.Context(), body.Username)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Same response shape and timing class as a wrong password, so the
			// endpoint does not confirm which usernames exist.
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}

	now := time.Now()
	claims := sessionClaims{
		UserID:   id,
		Username: body.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.tokenTTL)),
			Subject:   body.Username,
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not sign session")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  now.Add(a.tokenTTL),
		MaxAge:   int(a.tokenTTL.Seconds()),
	})
	_ = a.store.Users().TouchLogin(r.Context(), id)

	writeJSON(w, http.StatusOK, map[string]any{
		"username":   body.Username,
		"expires_at": now.Add(a.tokenTTL).UTC(),
	})
}

// Logout clears the cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Me reports the current session, or 401 when there is none.
func (a *Auth) Me(w http.ResponseWriter, r *http.Request) {
	claims, err := a.parse(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no active session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   claims.Username,
		"expires_at": claims.ExpiresAt.Time.UTC(),
	})
}

// RequireSession rejects requests without a valid session cookie. It also
// accepts a bearer token so the API can be driven from curl or a script.
func (a *Auth) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.parse(r); err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ActorFrom returns the username behind the request, for audit rows.
func (a *Auth) ActorFrom(r *http.Request) string {
	if claims, err := a.parse(r); err == nil {
		return claims.Username
	}
	return "anonymous"
}

func (a *Auth) parse(r *http.Request) (*sessionClaims, error) {
	raw := ""
	if cookie, err := r.Cookie(a.cookieName); err == nil {
		raw = cookie.Value
	}
	if raw == "" {
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			raw = strings.TrimPrefix(header, "Bearer ")
		}
	}
	if raw == "" {
		return nil, domain.ErrUnauthorized
	}

	claims := &sessionClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, domain.ErrUnauthorized
		}
		return a.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, domain.ErrUnauthorized
	}
	return claims, nil
}

// HashPassword produces the stored bcrypt hash.
func HashPassword(plain string) (string, error) {
	raw, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// constantTimeEqual is used where a plain comparison would leak length timing.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// contextKey is the private key type for request-scoped values.
type contextKey string

const actorKey contextKey = "actor"

// WithActor stores the acting username in the context.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}
