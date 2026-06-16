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
	"github.com/itsharsh007/openexchange/gateway/internal/cache"
	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/middleware"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("gateway: ")

	cfg, warnings := config.Load()
	for _, w := range warnings {
		log.Printf("WARN %s", w)
	}
	log.Printf("config %s", cfg)

	// Engine client. TODO: replace with engine.NewGRPCClient(cfg.EngineGRPCAddr)
	// once protoc stubs are generated. See internal/engine/grpc_client.go.
	eng := engine.NewMockClient()
	log.Printf("engine: using MOCK client (target gRPC addr=%s)", cfg.EngineGRPCAddr)

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

	// Middleware + routes.
	rl := middleware.NewRateLimiter(cfg.RateLimitPerSecond, cfg.RateLimitBurst)
	auth := middleware.NewJWTAuth(cfg.JWTSecret)
	srv := api.NewServer(cfg, eng, c, hub)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Routes(rl, auth),
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
