// Package config loads all runtime configuration from environment variables.
//
// WHY env-only: this follows the Twelve-Factor "config" principle. The same
// compiled binary/image is promoted unchanged from dev -> staging -> prod;
// only the environment differs. Nothing host- or secret-specific is ever
// baked into the code or the image, so there is no risk of leaking a real
// JWT secret in a git history or a Docker layer.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the fully-resolved configuration for the gateway. Every field has
// a sane default (except the JWT secret, which we *warn* about) so the service
// can boot with zero env vars during local development.
type Config struct {
	// ListenAddr is the host:port the HTTP server binds to.
	ListenAddr string
	// EngineGRPCAddr is the address of the Java matching engine's gRPC server.
	EngineGRPCAddr string
	// EngineMode selects the engine client: "grpc" (real engine, default) or "mock"
	// (in-process MockClient) so the gateway can run/demo standalone without the JVM.
	EngineMode string
	// RedisAddr is the Redis endpoint used to cache top-of-book snapshots.
	RedisAddr string
	// JWTSecret is the HMAC secret used to verify bearer tokens.
	JWTSecret string

	// RateLimitPerSecond is the sustained token-bucket refill rate per client.
	RateLimitPerSecond float64
	// RateLimitBurst is the maximum burst (bucket capacity) per client.
	RateLimitBurst int

	// EngineTimeout bounds every outbound call to the matching engine so a
	// slow/hung engine cannot pin a request goroutine forever.
	EngineTimeout time.Duration
	// CacheTTL is how long a cached book snapshot is considered fresh.
	CacheTTL time.Duration

	// KafkaBootstrap is the Kafka broker list (comma-separated) the trade-tape
	// consumer reads the `trades` topic from.
	KafkaBootstrap string
	// TradesTopic is the topic the engine publishes executed trades to.
	TradesTopic string
	// TapeConsumerGroup is this gateway's consumer group for the trade tape.
	// Each gateway replica joins the same group; partitions are shared across them.
	TapeConsumerGroup string
	// OrdersTopic is the topic the gateway publishes every order attempt to, for
	// the risk service's anomaly features (see internal/orderfeed).
	OrdersTopic string
	// SignalsTopic is the topic the risk service publishes RiskSignals to; the
	// gateway consumes it to gate orders from breaching accounts.
	SignalsTopic string
	// SignalsConsumerGroup is this gateway's consumer group for risk-signals.
	SignalsConsumerGroup string
}

// Load reads configuration from the environment, applying defaults. It never
// fails (returns no error) because a missing var should degrade to a default,
// not crash the edge service — but it does surface a warning string for any
// insecure default the operator should know about.
func Load() (*Config, []string) {
	var warnings []string

	port := getenv("PORT", "8080")
	jwtSecret := getenv("JWT_SECRET", "dev-only-change-me")
	if jwtSecret == "dev-only-change-me" {
		warnings = append(warnings,
			"JWT_SECRET is the insecure dev default; set a strong secret before exposing the gateway")
	}

	cfg := &Config{
		ListenAddr:         ":" + port,
		EngineGRPCAddr:     getenv("ENGINE_GRPC_ADDR", "localhost:50051"),
		EngineMode:         getenv("ENGINE_MODE", "grpc"),
		RedisAddr:          getenv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:          jwtSecret,
		RateLimitPerSecond: getenvFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst:     getenvInt("RATE_LIMIT_BURST", 40),
		EngineTimeout:      getenvDuration("ENGINE_TIMEOUT", 3*time.Second),
		CacheTTL:           getenvDuration("CACHE_TTL", 1*time.Second),
		KafkaBootstrap:     getenv("KAFKA_BOOTSTRAP", "localhost:9092"),
		TradesTopic:        getenv("TRADES_TOPIC", "trades"),
		TapeConsumerGroup:  getenv("TAPE_CONSUMER_GROUP", "gateway-tape"),
		OrdersTopic:          getenv("ORDERS_TOPIC", "orders"),
		SignalsTopic:         getenv("RISK_SIGNALS_TOPIC", "risk-signals"),
		SignalsConsumerGroup: getenv("RISK_CONSUMER_GROUP", "gateway-risk"),
	}
	return cfg, warnings
}

// String renders config for boot logging WITHOUT leaking the secret.
func (c *Config) String() string {
	return fmt.Sprintf(
		"listen=%s engine=%s redis=%s kafka=%s tradesTopic=%s tapeGroup=%s ordersTopic=%s signalsTopic=%s riskGroup=%s rps=%.1f burst=%d engineTimeout=%s cacheTTL=%s jwtSecret=<redacted>",
		c.ListenAddr, c.EngineGRPCAddr, c.RedisAddr,
		c.KafkaBootstrap, c.TradesTopic, c.TapeConsumerGroup, c.OrdersTopic,
		c.SignalsTopic, c.SignalsConsumerGroup,
		c.RateLimitPerSecond, c.RateLimitBurst, c.EngineTimeout, c.CacheTTL,
	)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
