// Package api wires the HTTP routes to the engine client, cache, and WS hub.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/itsharsh007/openexchange/gateway/internal/cache"
	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/metrics"
	"github.com/itsharsh007/openexchange/gateway/internal/middleware"
	"github.com/itsharsh007/openexchange/gateway/internal/orderfeed"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

// RiskGate reports whether an account is currently blocked by the risk service.
// An interface (not the concrete *risksignal.Gate) keeps handlers testable and lets
// callers pass AllowAllGate to run without a risk-signals feed.
type RiskGate interface {
	Blocked(accountID string) (bool, string)
}

// AllowAllGate is a RiskGate that never blocks — the default when no risk-signals
// consumer is wired (tests, or running the gateway standalone).
type AllowAllGate struct{}

func (AllowAllGate) Blocked(string) (bool, string) { return false, "" }

// Server bundles dependencies for the HTTP handlers.
type Server struct {
	cfg    *config.Config
	eng    engine.EngineClient
	cache  *cache.Cache
	hub    *ws.Hub
	orders orderfeed.Publisher
	risk   RiskGate
}

// NewServer constructs the API server with its dependencies injected. orders may be
// orderfeed.Noop and risk may be AllowAllGate{} to run without Kafka (e.g. in tests).
func NewServer(cfg *config.Config, eng engine.EngineClient, c *cache.Cache, hub *ws.Hub, orders orderfeed.Publisher, risk RiskGate) *Server {
	return &Server{cfg: cfg, eng: eng, cache: c, hub: hub, orders: orders, risk: risk}
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
	// Prometheus scrape endpoint — no auth so Prometheus can reach it without credentials.
	mux.Handle("GET /metrics", promhttp.Handler())

	// protect = rate limit -> JWT auth -> handler. Order matters: rate limit
	// first so unauthenticated floods are cheap to reject before crypto work.
	protect := func(h http.HandlerFunc) http.Handler {
		return rl.Middleware(auth.Middleware(h))
	}

	// Anonymous demo session: mints a short-lived bearer token so the public
	// dashboard works without a login flow. Rate-limited (no auth — that's the
	// point) and only registered when DEMO_AUTH_ENABLED is set.
	if s.cfg.DemoAuthEnabled {
		mux.Handle("POST /auth/demo", rl.Middleware(http.HandlerFunc(s.handleDemoSession)))
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
	start := time.Now()
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

	// Pre-trade risk gate: if the risk service has flagged this account (breaching a
	// limit), reject before touching the engine. The rejected attempt is still part
	// of the account's order flow, so publish it to the `orders` topic too.
	if blocked, reason := s.risk.Blocked(o.AccountID); blocked {
		metrics.RiskGateRejectTotal.Inc()
		metrics.OrderSubmitTotal.WithLabelValues("risk_blocked").Inc()
		metrics.OrderLatencySeconds.Observe(time.Since(start).Seconds())
		rejAck := engine.OrderAck{Status: engine.StatusRejected, Reason: "risk: " + reason}
		s.orders.PublishSubmit(o, rejAck)
		writeJSON(w, http.StatusUnprocessableEntity, rejAck)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.EngineTimeout)
	defer cancel()

	ack, err := s.eng.SubmitOrder(ctx, o)
	metrics.OrderLatencySeconds.Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.OrderSubmitTotal.WithLabelValues("gateway_error").Inc()
		writeErr(w, http.StatusBadGateway, "engine unavailable: "+err.Error())
		return
	}
	metrics.OrderSubmitTotal.WithLabelValues(string(ack.Status)).Inc()

	// Publish the order attempt + its outcome to the `orders` topic for the risk
	// service's anomaly features. Best-effort and async (see internal/orderfeed):
	// it runs only after the engine has acked and never affects this response.
	s.orders.PublishSubmit(o, ack)

	// Push a fresh book snapshot to all WS clients so the ladder updates
	// immediately after an order touches the book. Done in a goroutine so it
	// never delays the HTTP response. Best-effort: engine/cache errors are
	// logged and ignored.
	go s.broadcastBook(o.Symbol)

	// NOTE: the real-time trade tape is NOT synthesized here anymore. Every
	// executed trade — including the resting (maker) side that never called this
	// gateway — is published by the engine to the Kafka `trades` topic and fanned
	// out to all dashboards by internal/tape.TradeConsumer. Synthesizing a trade
	// from this one submitter's ack would miss the maker side and could disagree
	// with the engine's authoritative price.

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

	// A cancel is part of an account's order flow too (rapid cancels are a classic
	// spoofing signal), so publish it to the `orders` topic — best-effort, async.
	s.orders.PublishCancel(cancelReq, ack)

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

// broadcastBook fetches the latest book snapshot for symbol and pushes it to all
// WS clients so the depth ladder refreshes without a client poll. Called in a
// goroutine — errors are logged and never surface to callers.
func (s *Server) broadcastBook(symbol string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.EngineTimeout)
	defer cancel()

	const broadcastDepth = 20
	var snap engine.BookSnapshot
	var err error
	if cached, ok := s.cache.GetBook(ctx, symbol, broadcastDepth); ok {
		snap = cached
	} else {
		snap, err = s.eng.GetBook(ctx, engine.BookRequest{Symbol: symbol, Depth: broadcastDepth})
		if err != nil {
			log.Printf("api: broadcastBook %s: %v", symbol, err)
			return
		}
		s.cache.SetBook(ctx, broadcastDepth, snap)
	}
	s.hub.Broadcast(struct {
		Type string              `json:"type"`
		Data engine.BookSnapshot `json:"data"`
	}{Type: "book", Data: snap})
}

// handleDemoSession: POST /auth/demo — issues a short-lived HS256 bearer token
// for an anonymous demo account. This is the "real auth" surface for a public
// playground: no password (it's play money on a mock engine), but a proper
// signed, expiring token rather than a static secret baked into the frontend.
// A login-backed issuer would slot in here unchanged — same token shape.
func (s *Server) handleDemoSession(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(s.cfg.DemoSessionTTL)
	claims := jwt.RegisteredClaims{
		Subject:   s.cfg.DemoAccountID,
		ExpiresAt: jwt.NewNumericDate(exp),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not mint demo token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":            signed,
		"accountId":        s.cfg.DemoAccountID,
		"expiresInSeconds": int(s.cfg.DemoSessionTTL.Seconds()),
	})
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
