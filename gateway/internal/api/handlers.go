// Package api wires the HTTP routes to the engine client, cache, and WS hub.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/itsharsh007/openexchange/gateway/internal/cache"
	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/middleware"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

// Server bundles dependencies for the HTTP handlers.
type Server struct {
	cfg   *config.Config
	eng   engine.EngineClient
	cache *cache.Cache
	hub   *ws.Hub
}

// NewServer constructs the API server with its dependencies injected.
func NewServer(cfg *config.Config, eng engine.EngineClient, c *cache.Cache, hub *ws.Hub) *Server {
	return &Server{cfg: cfg, eng: eng, cache: c, hub: hub}
}

// Routes builds the http.Handler with middleware applied per-route.
//
// WHY per-route middleware: /healthz must be unauthenticated (load balancers
// probe it without credentials) and unmetered, while everything else gets JWT
// auth + rate limiting. We compose handlers explicitly rather than using one
// global chain so these exceptions are obvious.
func (s *Server) Routes(rl *middleware.RateLimiter, auth *middleware.JWTAuth) http.Handler {
	mux := http.NewServeMux()

	// Public, unauthenticated liveness/readiness probe.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// protect = rate limit -> JWT auth -> handler. Order matters: rate limit
	// first so unauthenticated floods are cheap to reject before crypto work.
	protect := func(h http.HandlerFunc) http.Handler {
		return rl.Middleware(auth.Middleware(h))
	}

	mux.Handle("POST /orders", protect(s.handleSubmit))
	mux.Handle("DELETE /orders/{id}", protect(s.handleCancel))
	mux.Handle("GET /book/{symbol}", protect(s.handleBook))
	mux.Handle("GET /ws", protect(http.HandlerFunc(s.hub.ServeWS)))

	return mux
}

// handleHealth reports liveness plus a best-effort Redis status. It always
// returns 200 if the process is up; Redis being down is reported, not fatal.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"redis":  s.cache.Ping(ctx),
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handleSubmit: POST /orders — accepts a NewOrder JSON, forwards to the engine,
// and on a fill broadcasts a synthetic trade to WS subscribers.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var o engine.NewOrder
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&o); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Attribute the order to the authenticated account (don't trust the body).
	if acct := middleware.AccountID(r.Context()); acct != "" {
		o.AccountID = acct
	}
	if o.Symbol == "" {
		writeErr(w, http.StatusBadRequest, "symbol required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.EngineTimeout)
	defer cancel()

	ack, err := s.eng.SubmitOrder(ctx, o)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine unavailable: "+err.Error())
		return
	}

	// On a fill, push a trade event to all dashboards in real time.
	if ack.Status == engine.StatusFilled || ack.Status == engine.StatusPartiallyFilled {
		s.hub.Broadcast(map[string]any{
			"type": "trade",
			"trade": engine.Trade{
				TradeID:    "trade-" + ack.OrderID,
				Symbol:     o.Symbol,
				PriceTicks: o.PriceTicks,
				Quantity:   ack.FilledQuantity,
				TsMillis:   time.Now().UnixMilli(),
			},
		})
	}

	status := http.StatusCreated
	if ack.Status == engine.StatusRejected {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, ack)
}

// handleCancel: DELETE /orders/{id}
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "order id required")
		return
	}
	cancelReq := engine.CancelOrder{
		OrderID:   id,
		Symbol:    r.URL.Query().Get("symbol"),
		AccountID: middleware.AccountID(r.Context()),
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.EngineTimeout)
	defer cancel()

	ack, err := s.eng.CancelOrder(ctx, cancelReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine unavailable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

// handleBook: GET /book/{symbol}?depth=N — cache-aside read.
func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	symbol := r.PathValue("symbol")
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "symbol required")
		return
	}
	depth := int32(5)
	if d := r.URL.Query().Get("depth"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			depth = int32(n)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.EngineTimeout)
	defer cancel()

	// Cache-aside: try Redis first; on a miss, ask the engine and backfill.
	if snap, ok := s.cache.GetBook(ctx, symbol, depth); ok {
		w.Header().Set("X-Cache", "HIT")
		writeJSON(w, http.StatusOK, snap)
		return
	}

	snap, err := s.eng.GetBook(ctx, engine.BookRequest{Symbol: symbol, Depth: depth})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine unavailable: "+err.Error())
		return
	}
	s.cache.SetBook(ctx, depth, snap)
	w.Header().Set("X-Cache", "MISS")
	writeJSON(w, http.StatusOK, snap)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
