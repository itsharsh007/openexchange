// Command gateway is the HTTP/WebSocket/gRPC edge of OpenExchange.
//
// Boot sequence:
//  1. Load config from env (Twelve-Factor).
//  2. Wire the engine client (MockClient today; see internal/engine/grpc_client.go
//     for how to swap in the real gRPC client).
//  3. Connect Redis (non-fatal if down — graceful degradation).
//  4. Start the WS hub goroutine.
//  5. Serve HTTP with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/itsharsh007/openexchange/gateway/internal/api"
	"github.com/itsharsh007/openexchange/gateway/internal/auth"
	"github.com/itsharsh007/openexchange/gateway/internal/cache"
	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/middleware"
	"github.com/itsharsh007/openexchange/gateway/internal/orderfeed"
	"github.com/itsharsh007/openexchange/gateway/internal/risksignal"
	"github.com/itsharsh007/openexchange/gateway/internal/tape"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

func main() {
	// Self-healthcheck mode: `gateway healthcheck` GETs /healthz and exits 0/1.
	// WHY: the runtime image is distroless (no shell, no wget/curl), so the
	// container HEALTHCHECK invokes this same static binary instead of an external
	// tool. Reads PORT (default 8080) to match the listen address.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://localhost:" + port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("gateway: ")

	cfg, warnings := config.Load()
	for _, w := range warnings {
		log.Printf("WARN %s", w)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("config %s", cfg)

	// Demo symbols traded on the public link / standalone gateway.
	demoSymbols := []string{"AAPL", "TSLA", "MSFT"}

	// Engine client: real gRPC adapter to the Java matching engine, or an in-process
	// mock (ENGINE_MODE=mock) so the gateway can run standalone for demos/local dev.
	var eng engine.EngineClient
	localEngine := cfg.EngineMode == "mock" || cfg.EngineMode == "local"
	if localEngine {
		// In-process matching engine: a real, shared limit order book so the gateway
		// runs a multiplayer exchange standalone (no JVM/Kafka). Seeded with starter
		// liquidity for the demo symbols so the first visitor sees a populated book.
		eng = engine.NewLocalEngine(demoSymbols...)
		log.Printf("engine: LOCAL in-process matching engine (ENGINE_MODE=%s) — real matching, seeded book", cfg.EngineMode)
	} else {
		// grpc.NewClient is lazy, so this does not block on a live engine — each RPC
		// carries a per-call deadline (cfg.EngineTimeout) enforced by the handlers.
		gc, err := engine.NewGRPCClient(cfg.EngineGRPCAddr)
		if err != nil {
			log.Fatalf("engine: failed to create gRPC client for %s: %v", cfg.EngineGRPCAddr, err)
		}
		defer gc.Close()
		eng = gc
		log.Printf("engine: gRPC client -> %s", cfg.EngineGRPCAddr)
	}

	// Redis — non-fatal if unavailable.
	ctx := context.Background()
	c, healthy := cache.New(ctx, cfg.RedisAddr, cfg.CacheTTL)
	if healthy {
		log.Printf("redis: connected at %s", cfg.RedisAddr)
	} else {
		log.Printf("WARN redis: not reachable at %s — running without cache (degraded)", cfg.RedisAddr)
	}
	defer c.Close()

	// WebSocket hub.
	hub := ws.NewHub()
	go hub.Run()

	// Trade tape: consume the engine's Kafka `trades` topic and fan each real
	// trade out to every connected dashboard. Non-fatal if Kafka is down — the
	// reader reconnects with backoff, so REST/WS keep serving meanwhile.
	tapeCtx, stopTape := context.WithCancel(context.Background())
	tradeTape := tape.NewTradeConsumer(
		[]string{cfg.KafkaBootstrap}, cfg.TradesTopic, cfg.TapeConsumerGroup, hub)
	go tradeTape.Run(tapeCtx)
	defer func() {
		stopTape()
		_ = tradeTape.Close()
	}()

	// Order feed: publish every order attempt to the Kafka `orders` topic for the
	// risk service's anomaly features. Best-effort + async (see internal/orderfeed)
	// — a broker outage degrades risk features, never order handling.
	orderPub := orderfeed.NewKafkaPublisher([]string{cfg.KafkaBootstrap}, cfg.OrdersTopic)
	defer func() { _ = orderPub.Close() }()

	// Risk gate: consume the risk service's `risk-signals` topic into an in-memory
	// per-account gate that the order path checks before forwarding to the engine.
	// Fails open if Kafka/risk is down (gate stays empty -> nothing blocked).
	riskGate := risksignal.NewGate()
	riskCtx, stopRisk := context.WithCancel(context.Background())
	riskConsumer := risksignal.NewConsumer(
		[]string{cfg.KafkaBootstrap}, cfg.SignalsTopic, cfg.SignalsConsumerGroup, riskGate, hub)
	go riskConsumer.Run(riskCtx)
	defer func() {
		stopRisk()
		_ = riskConsumer.Close()
	}()

	// Middleware + routes.
	rl := middleware.NewRateLimiter(cfg.RateLimitPerSecond, cfg.RateLimitBurst)
	jwtAuth := middleware.NewJWTAuth(cfg.JWTSecret)
	srv := api.NewServer(cfg, eng, c, hub, orderPub, riskGate)

	// Password auth (signup/login/refresh) — only when a Postgres DSN is set.
	// The public link runs without it and keeps the anonymous guest path.
	if cfg.AuthEnabled() {
		db, err := auth.OpenDB(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("auth: %v", err)
		}
		defer db.Close()
		if err := db.Ping(); err != nil {
			log.Fatalf("auth: cannot reach Postgres for user store: %v", err)
		}
		tokens := auth.NewTokenService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
		srv.WithAuth(auth.NewPostgresStore(db), tokens).WithDB(db)
		log.Printf("auth: password auth ENABLED (Postgres user store)")
	} else {
		log.Printf("auth: password auth disabled (no DATABASE_URL) — guest/demo only")
	}

	// Market simulator: bot accounts that keep the in-process book alive (moving
	// price, streaming tape, populated depth) so the public link is impressive with
	// zero visitors. Only for the LocalEngine; the full stack uses `make seed`.
	if localEngine && cfg.MarketSim {
		simCtx, stopSim := context.WithCancel(context.Background())
		srv.StartMarketSim(simCtx, demoSymbols, cfg.MarketSimInterval, time.Now().UnixNano())
		defer stopSim()
		log.Printf("market sim: ENABLED (%d symbols, every %s)", len(demoSymbols), cfg.MarketSimInterval)
	}

	// Middleware chain (outermost first): security headers -> CORS -> routes.
	handler := middleware.SecurityHeaders(middleware.CORS(cfg.CORSAllowedOrigin)(srv.Routes(rl, jwtAuth)))
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second, // mitigate Slowloris
	}

	// Run the server in a goroutine so main can wait for shutdown signals.
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown: stop accepting, drain in-flight requests, then exit.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Printf("bye")
}
