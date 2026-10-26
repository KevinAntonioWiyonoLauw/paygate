// Package api is the HTTP surface: JSON endpoints, SSE status streaming, cookie
// auth, and the QR image endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hirotomasato/paygateme/shopee"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
	"github.com/kevinantoniowiyonolauw/paygate/internal/order"
	"github.com/kevinantoniowiyonolauw/paygate/internal/qris"
	"github.com/kevinantoniowiyonolauw/paygate/internal/store/postgres"
)

// Deps is everything the router needs.
type Deps struct {
	Store    *postgres.Store
	Orders   *order.Service
	Auth     *Auth
	Logger   *slog.Logger
	Login    LoginService
	Version  string
	PublicURL string
	// OnSessionReady is called after a successful OTP login so the caller can
	// spin up the polling worker.
	OnSessionReady func(session *shopee.Session)
}

// LoginService is the OTP login surface the API needs.
type LoginService interface {
	RequestOTP(ctx context.Context, phone, password, deviceReport string) (*shopee.OtpChallenge, error)
	VerifyOTP(ctx context.Context, otp, merchantID, storeID string) (*shopee.Session, []shopee.MerchantSummary, error)
	PersistSession(ctx context.Context, session *shopee.Session) error
}

// Server wires the router.
type Server struct {
	deps Deps
	log  *slog.Logger
}

// New builds the HTTP server.
func New(deps Deps) *Server {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{deps: deps, log: log}
}

// Router returns the fully configured handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.recoverer)
	r.Use(s.requestLogger)
	r.Use(corsFor(s.deps.PublicURL))

	r.Get("/healthz", s.health)

	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", s.deps.Auth.Login)
		r.Post("/auth/logout", s.deps.Auth.Logout)
		r.Get("/auth/me", s.deps.Auth.Me)

		// Public: a buyer's browser polls the order it was given.
		r.Get("/orders/{id}/status", s.orderStatusSSE)
		r.Get("/qr/{id}.png", s.qrPNG)
		r.Get("/qr/{id}.svg", s.qrSVG)
		r.Get("/public/orders/{id}", s.publicOrder)

		// Authenticated surface.
		r.Group(func(r chi.Router) {
			r.Use(s.deps.Auth.RequireSession)

			r.Get("/stats", s.stats)
			r.Get("/orders", s.listOrders)
			r.Post("/orders", s.createOrder)
			r.Get("/orders/{id}", s.getOrder)
			r.Post("/orders/{id}/cancel", s.cancelOrder)

			r.Get("/payments", s.listPayments)
			r.Post("/payments/{id}/match", s.matchPayment)

			r.Get("/qris/inspect", s.inspectQRIS)
			r.Get("/qris/static", s.getStaticQRIS)
			r.Put("/qris/static", s.putStaticQRIS)

			r.Get("/provider/status", s.providerStatus)
			r.Post("/provider/login/request", s.requestOTP)
			r.Post("/provider/login/verify", s.verifyOTP)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint does not exist")
	})
	return r
}

// ---- middleware ----

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "recover", rec)
				writeError(w, http.StatusInternalServerError, "internal", "unexpected error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Health checks and SSE polling would drown the log.
		if r.URL.Path == "/healthz" || isSSE(r.URL.Path) {
			return
		}
		s.log.Debug("http",
			"method", r.Method, "path", r.URL.Path,
			"status", ww.Status(), "dur", time.Since(start).String())
	})
}

func isSSE(path string) bool {
	return len(path) > 7 && path[len(path)-7:] == "/status"
}

func corsFor(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- handlers ----

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.deps.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded", "database": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": s.deps.Version, "time": time.Now().UTC(),
	})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.deps.Store.Orders().Stats(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	filter := postgres.ListFilter{
		Search: r.URL.Query().Get("q"),
		Limit:  queryInt(r, "limit", 50),
		Offset: queryInt(r, "offset", 0),
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, part := range splitCSV(raw) {
			filter.Status = append(filter.Status, domain.OrderStatus(part))
		}
	}
	orders, total, err := s.deps.Store.Orders().List(r.Context(), filter)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders, "total": total})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.deps.Store.Orders().GetByPublicID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// publicOrder is the buyer-facing view: no metadata, no internal ids.
func (s *Server) publicOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.deps.Store.Orders().GetByPublicID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"public_id":    o.PublicID,
		"total_amount": o.TotalAmount,
		"status":       o.Status,
		"description":  o.Description,
		"expires_at":   o.ExpiresAt,
		"paid_at":      o.PaidAt,
	})
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var in order.CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	// Actor comes from the session, never from the body.
	in.Actor = s.deps.Auth.ActorFrom(r)
	created, err := s.deps.Orders.Create(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	actor := s.deps.Auth.ActorFrom(r)
	updated, err := s.deps.Orders.Cancel(r.Context(), chi.URLParam(r, "id"), actor)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	filter := postgres.PaymentListFilter{
		UnmatchedOnly: r.URL.Query().Get("unmatched") == "1",
		Limit:         queryInt(r, "limit", 50),
		Offset:        queryInt(r, "offset", 0),
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, part := range splitCSV(raw) {
			filter.Status = append(filter.Status, domain.PaymentStatus(part))
		}
	}
	payments, total, err := s.deps.Store.Payments().List(r.Context(), filter)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": payments, "total": total})
}

func (s *Server) matchPayment(w http.ResponseWriter, r *http.Request) {
	paymentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", "payment id must be numeric")
		return
	}
	var body struct {
		OrderPublicID string `json:"order_public_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	o, err := s.deps.Store.Orders().GetByPublicID(r.Context(), body.OrderPublicID)
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.deps.Auth.ActorFrom(r)
	if err := s.deps.Store.Payments().MatchManually(r.Context(), paymentID, o.ID, actor); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) inspectQRIS(w http.ResponseWriter, r *http.Request) {
	payload := r.URL.Query().Get("payload")
	if payload == "" {
		writeError(w, http.StatusBadRequest, "missing_payload", "payload query parameter is required")
		return
	}
	writeJSON(w, http.StatusOK, qris.Describe(payload))
}

func (s *Server) getStaticQRIS(w http.ResponseWriter, r *http.Request) {
	payload := s.deps.Orders.StaticQRIS()
	writeJSON(w, http.StatusOK, map[string]any{
		"payload": payload,
		"bound":   payload != "",
		"info":    qris.Describe(payload),
	})
}

func (s *Server) putStaticQRIS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	if err := s.deps.Orders.SetStaticQRIS(r.Context(), body.Payload); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bound": s.deps.Orders.StaticQRIS() != "",
		"info":  qris.Describe(s.deps.Orders.StaticQRIS()),
	})
}

func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request) {
	payload, ok := s.lookupQRPayload(w, r)
	if !ok {
		return
	}
	size := queryInt(r, "size", 512)
	raw, err := qris.RenderPNG(payload, qris.RenderOptions{Size: size})
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(raw)
}

func (s *Server) qrSVG(w http.ResponseWriter, r *http.Request) {
	payload, ok := s.lookupQRPayload(w, r)
	if !ok {
		return
	}
	raw, err := qris.RenderSVG(payload, queryInt(r, "module", 8))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(raw)
}

func (s *Server) lookupQRPayload(w http.ResponseWriter, r *http.Request) (string, bool) {
	o, err := s.deps.Store.Orders().GetByPublicID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return "", false
	}
	if o.QRPayload == "" {
		writeError(w, http.StatusNotFound, "no_qr", "order has no QR payload")
		return "", false
	}
	return o.QRPayload, true
}

// ---- helpers ----

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrInvalidState):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrPoolExhausted):
		writeError(w, http.StatusServiceUnavailable, "pool_exhausted", err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
	default:
		s.log.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"code": code, "message": message,
	}})
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func splitCSV(raw string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			if part := raw[start:i]; part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
