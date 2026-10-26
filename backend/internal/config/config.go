// Package config loads and validates the gateway configuration from the
// environment. Every value has a safe default except credentials, which must be
// supplied explicitly so a misconfigured deploy fails loudly at boot instead of
// silently running with a placeholder secret.
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved gateway configuration.
type Config struct {
	Addr      string
	PublicURL string

	DB       DBConfig
	Auth     AuthConfig
	Crypto   CryptoConfig
	Shopee   ShopeeConfig
	LogLevel string
}

// DBConfig holds Postgres connection settings.
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	MaxConns int32
}

// DSN returns a libpq-style connection string for pgx.
func (c DBConfig) DSN() string {
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, ssl,
	)
}

// AuthConfig holds dashboard authentication settings.
type AuthConfig struct {
	JWTSecret   []byte
	AdminUser   string
	AdminPass   string
	TokenTTL    time.Duration
	CookieName  string
	SecureCookie bool
}

// CryptoConfig holds the key used to encrypt provider sessions at rest.
type CryptoConfig struct {
	Key []byte
}

// ShopeeConfig holds provider tuning.
type ShopeeConfig struct {
	Phone          string
	Password       string
	DeviceReport   string
	StaticQRIS     string
	PollInterval   time.Duration
	Expiry         time.Duration
	ExpiryTick     time.Duration
	ClockSkew      time.Duration
	MaxOffset      int
	OffsetCooldown time.Duration
}

// Load reads the environment and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:      env("PAYGATE_ADDR", "127.0.0.1:8787"),
		PublicURL: strings.TrimRight(env("PAYGATE_PUBLIC_URL", "http://127.0.0.1:8787"), "/"),
		LogLevel:  env("PAYGATE_LOG_LEVEL", "info"),
		DB: DBConfig{
			Host:     env("POSTGRES_HOST", "127.0.0.1"),
			Port:     envInt("POSTGRES_PORT", 5433),
			User:     env("POSTGRES_USER", "paygate"),
			Password: env("POSTGRES_PASSWORD", ""),
			Database: env("POSTGRES_DB", "paygate"),
			SSLMode:  env("POSTGRES_SSLMODE", "disable"),
			MaxConns: int32(envInt("POSTGRES_MAX_CONNS", 10)),
		},
		Shopee: ShopeeConfig{
			Phone:          env("SHOPEE_PHONE", ""),
			Password:       env("SHOPEE_PASSWORD", ""),
			DeviceReport:   env("SHOPEE_DEVICE_REPORT", ""),
			StaticQRIS:     env("SHOPEE_STATIC_QRIS", ""),
			PollInterval:   time.Duration(envInt("SHOPEE_POLL_INTERVAL_MS", 2000)) * time.Millisecond,
			Expiry:         time.Duration(envInt("SHOPEE_EXPIRY_SECONDS", 900)) * time.Second,
			ExpiryTick:     time.Duration(envInt("PAYGATE_EXPIRY_TICK_SECONDS", 15)) * time.Second,
			ClockSkew:      time.Duration(envInt("SHOPEE_CLOCK_SKEW_SECONDS", 90)) * time.Second,
			MaxOffset:      envInt("SHOPEE_MAX_OFFSET", 999),
			OffsetCooldown: time.Duration(envInt("SHOPEE_OFFSET_COOLDOWN_HOURS", 24)) * time.Hour,
		},
	}

	jwtSecret, err := envHexKey("PAYGATE_JWT_SECRET", 32)
	if err != nil {
		return nil, err
	}
	encKey, err := envHexKey("PAYGATE_ENCRYPTION_KEY", 32)
	if err != nil {
		return nil, err
	}

	cfg.Auth = AuthConfig{
		JWTSecret:    jwtSecret,
		AdminUser:    env("PAYGATE_ADMIN_USER", "admin"),
		AdminPass:    env("PAYGATE_ADMIN_PASSWORD", ""),
		TokenTTL:     time.Duration(envInt("PAYGATE_TOKEN_TTL_HOURS", 168)) * time.Hour,
		CookieName:   "paygate_session",
		SecureCookie: envBool("PAYGATE_SECURE_COOKIE", false),
	}
	cfg.Crypto = CryptoConfig{Key: encKey}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DB.Password == "" {
		return fmt.Errorf("POSTGRES_PASSWORD is required")
	}
	if c.Auth.AdminPass == "" {
		return fmt.Errorf("PAYGATE_ADMIN_PASSWORD is required (min 8 chars)")
	}
	if len(c.Auth.AdminPass) < 8 {
		return fmt.Errorf("PAYGATE_ADMIN_PASSWORD must be at least 8 characters")
	}
	if c.Shopee.MaxOffset < 1 {
		return fmt.Errorf("SHOPEE_MAX_OFFSET must be >= 1")
	}
	if c.Shopee.Expiry < 60*time.Second {
		return fmt.Errorf("SHOPEE_EXPIRY_SECONDS must be >= 60")
	}
	return nil
}

// ---- env helpers ----

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// envHexKey reads a hex-encoded key and enforces its decoded length.
func envHexKey(key string, wantBytes int) ([]byte, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return nil, fmt.Errorf("%s is required (generate with: openssl rand -hex %d)", key, wantBytes)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s must be hex-encoded: %w", key, err)
	}
	if len(decoded) != wantBytes {
		return nil, fmt.Errorf("%s must decode to %d bytes, got %d", key, wantBytes, len(decoded))
	}
	return decoded, nil
}
