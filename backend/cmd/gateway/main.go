// Command gateway runs the paygate QRIS gateway: HTTP API, reconciliation
// worker, and static file serving for the dashboard build.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hirotomasato/paygateme/shopee"

	"github.com/kevinantoniowiyonolauw/paygate/internal/api"
	"github.com/kevinantoniowiyonolauw/paygate/internal/config"
	"github.com/kevinantoniowiyonolauw/paygate/internal/order"
	"github.com/kevinantoniowiyonolauw/paygate/internal/provider"
	"github.com/kevinantoniowiyonolauw/paygate/internal/reconcile"
	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

var version = "0.1.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate-only", false, "apply migrations and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(ctx, cfg.DB.DSN(), cfg.DB.MaxConns)
	if err != nil {
		return err
	}
	defer store.Close()
	log.Info("database connected", "host", cfg.DB.Host, "port", cfg.DB.Port, "db", cfg.DB.Database)

	applied, err := store.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "files", applied)
	} else {
		log.Info("schema up to date")
	}

	// Bootstrap the admin account. Only creates; never overwrites a password.
	hash, err := api.HashPassword(cfg.Auth.AdminPass)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	created, err := store.Users().EnsureAdmin(ctx, cfg.Auth.AdminUser, hash)
	if err != nil {
		return err
	}
	if created {
		log.Info("admin user created", "username", cfg.Auth.AdminUser)
	}

	if *migrateOnly {
		log.Info("migrate-only: done")
		return nil
	}

	// Order service: static QRIS is restored from the database, then the env
	// value is applied only when the database has none (first boot).
	orderSvc := order.New(order.Options{
		Store:          store,
		Provider:       provider.ProviderID,
		MaxOffset:      cfg.Shopee.MaxOffset,
		Expiry:         cfg.Shopee.Expiry,
		OffsetCooldown: cfg.Shopee.OffsetCooldown,
	})
	if err := orderSvc.LoadStaticQRIS(ctx); err != nil {
		return err
	}
	if orderSvc.StaticQRIS() == "" && cfg.Shopee.StaticQRIS != "" {
		if err := orderSvc.SetStaticQRIS(ctx, cfg.Shopee.StaticQRIS); err != nil {
			log.Warn("SHOPEE_STATIC_QRIS rejected", "error", err)
		} else {
			log.Info("static QRIS bound from environment")
		}
	}
	if orderSvc.StaticQRIS() == "" {
		log.Warn("no static QRIS bound; order creation will fail until one is set in Settings")
	}

	loginSvc, err := provider.NewLoginService(provider.LoginOptions{
		Store:         store,
		EncryptionKey: cfg.Crypto.Key,
		DeviceReport:  cfg.Shopee.DeviceReport,
	})
	if err != nil {
		return err
	}

	// The polling worker only runs once a provider session exists. It is started
	// lazily on login, and on boot when a stored session is still valid.
	var workerMu sync.Mutex
	var worker *reconcile.Worker

	// Housekeeping runs regardless of provider state: expiring abandoned orders
	// and pruning stale reservations is local work, and leaving it tied to a
	// Shopee session would strand pending orders indefinitely.
	housekeeper := reconcile.New(reconcile.Options{
		Store:      store,
		Logger:     log,
		ExpiryTick: cfg.Shopee.ExpiryTick,
	})
	housekeeper.StartExpiry(ctx)

	startWorker := func(session *shopee.Session) {
		workerMu.Lock()
		defer workerMu.Unlock()
		if worker != nil {
			return
		}
		adapter, err := provider.NewAdapter(session)
		if err != nil {
			log.Error("provider adapter", "error", err)
			return
		}
		worker = reconcile.New(reconcile.Options{
			Store:        store,
			Provider:     adapter,
			Logger:       log,
			PollInterval: cfg.Shopee.PollInterval,
			ClockSkew:    cfg.Shopee.ClockSkew,
		})
		worker.Start(ctx)
		log.Info("reconciliation worker started",
			"merchant", adapter.MerchantName(), "store", adapter.StoreID(),
			"poll", cfg.Shopee.PollInterval.String())
	}

	if session, err := loginSvc.LoadSession(ctx); err == nil && session != nil {
		if session.ExpiresAt == 0 || time.Now().Before(time.UnixMilli(session.ExpiresAt)) {
			startWorker(session)
		} else {
			log.Warn("stored Shopee session expired; log in again from the dashboard")
		}
	} else if err != nil && !errors.Is(err, errors.New("not found")) {
		log.Debug("no stored provider session", "reason", err)
	}

	srv := api.New(api.Deps{
		Store:          store,
		Orders:         orderSvc,
		Auth:           api.NewAuth(api.AuthOptions{
			Store:        store,
			Secret:       cfg.Auth.JWTSecret,
			CookieName:   cfg.Auth.CookieName,
			TokenTTL:     cfg.Auth.TokenTTL,
			SecureCookie: cfg.Auth.SecureCookie,
		}),
		Logger:         log,
		Login:          apiLoginAdapter{svc: loginSvc},
		Version:        version,
		PublicURL:      cfg.PublicURL,
		OnSessionReady: startWorker,
	})

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // SSE streams must not be cut by a write deadline
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "public_url", cfg.PublicURL, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "error", err)
	}
	workerMu.Lock()
	if worker != nil {
		worker.Stop()
	}
	workerMu.Unlock()
	housekeeper.Stop()
	log.Info("stopped")
	return nil
}

// apiLoginAdapter narrows the login service to the interface the API declares,
// keeping internal/provider free of HTTP concerns.
type apiLoginAdapter struct{ svc *provider.LoginService }

func (a apiLoginAdapter) RequestOTP(ctx context.Context, phone, password, deviceReport string) (*shopee.OtpChallenge, error) {
	return a.svc.RequestOTP(ctx, phone, password, deviceReport)
}

func (a apiLoginAdapter) VerifyOTP(ctx context.Context, otp, merchantID, storeID string) (*shopee.Session, []shopee.MerchantSummary, error) {
	return a.svc.VerifyOTP(ctx, otp, merchantID, storeID)
}

func (a apiLoginAdapter) PersistSession(ctx context.Context, session *shopee.Session) error {
	return a.svc.PersistSession(ctx, session)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
