package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kevinantoniowiyonolauw/paygate/internal/domain"
)

// orderStatusSSE streams order status changes until the order is terminal or
// the client disconnects. Polling the DB is enough at this scale: one query per
// second per open tab, against a primary key lookup.
func (s *Server) orderStatusSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no_sse", "streaming unsupported")
		return
	}

	publicID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Verify the order exists before opening a stream, so a bad id is a clean 404.
	if _, err := s.deps.Store.Orders().GetByPublicID(ctx, publicID); err != nil {
		s.fail(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, payload any) bool {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Initial snapshot so the client renders immediately.
	o, err := s.deps.Store.Orders().GetByPublicID(ctx, publicID)
	if err != nil {
		return
	}
	if !send("status", snapshot(o)) {
		return
	}
	if o.Terminal() {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	var last domain.OrderStatus = o.Status
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			current, err := s.deps.Store.Orders().GetByPublicID(ctx, publicID)
			if err != nil {
				return
			}
			if current.Status != last {
				last = current.Status
				if !send("status", snapshot(current)) {
					return
				}
			}
			if current.Terminal() {
				return
			}
		}
	}
}

func snapshot(o domain.Order) map[string]any {
	return map[string]any{
		"public_id":    o.PublicID,
		"status":       o.Status,
		"total_amount": o.TotalAmount,
		"expires_at":   o.ExpiresAt,
		"paid_at":      o.PaidAt,
	}
}

// providerStatus reports whether a live session is available.
func (s *Server) providerStatus(w http.ResponseWriter, r *http.Request) {
	row, err := s.deps.Store.Sessions().Load(r.Context(), "shopee")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":     "shopee",
			"authenticated": false,
			"reason":       "no session stored; log in with an OTP",
		})
		return
	}
	expired := row.ExpiresAt != nil && time.Now().After(*row.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":      "shopee",
		"authenticated": !expired,
		"expired":       expired,
		"merchant_id":   row.MerchantID,
		"merchant_name": row.MerchantName,
		"store_id":      row.StoreID,
		"store_name":    row.StoreName,
		"expires_at":    row.ExpiresAt,
		"updated_at":    row.UpdatedAt,
	})
}

// requestOTP starts a Shopee login. The challenge is held server-side in memory
// for a short window; it is never returned to the browser.
func (s *Server) requestOTP(w http.ResponseWriter, r *http.Request) {
	if s.deps.Login == nil {
		writeError(w, http.StatusServiceUnavailable, "no_login", "login is not configured")
		return
	}
	var body struct {
		Phone        string `json:"phone"`
		Password     string `json:"password"`
		DeviceReport string `json:"device_report"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	challenge, err := s.deps.Login.RequestOTP(r.Context(), body.Phone, body.Password, body.DeviceReport)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent":     true,
		"channel":  challenge.Channel,
		"channels": challenge.AvailableChannels,
		"has_password": challenge.HasPassword,
	})
}

// verifyOTP completes a login and persists the encrypted session.
func (s *Server) verifyOTP(w http.ResponseWriter, r *http.Request) {
	if s.deps.Login == nil {
		writeError(w, http.StatusServiceUnavailable, "no_login", "login is not configured")
		return
	}
	var body struct {
		OTP        string `json:"otp"`
		MerchantID string `json:"merchant_id"`
		StoreID    string `json:"store_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	session, merchants, err := s.deps.Login.VerifyOTP(r.Context(), body.OTP, body.MerchantID, body.StoreID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if session == nil {
		// Multiple merchants on the account: the operator must choose one.
		writeJSON(w, http.StatusOK, map[string]any{
			"needs_merchant": true,
			"merchants":      merchants,
		})
		return
	}

	if err := s.deps.Login.PersistSession(r.Context(), session); err != nil {
		s.fail(w, err)
		return
	}
	// A fresh session means the polling worker can start.
	if s.deps.OnSessionReady != nil {
		s.deps.OnSessionReady(session)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"merchant_name": session.Merchant.Name,
		"store_id":      session.StoreID,
		"expires_at":    time.UnixMilli(session.ExpiresAt).UTC(),
	})
}

var _ = context.Background
