#!/usr/bin/env bash
# Seed demo accounts, Kafka topics, and a small burst of orders.
#
# Usage:
#   make seed               → runs against the docker-compose stack (localhost)
#   GATEWAY=http://host:8080 make seed  → run against any gateway
#
# Prerequisites: docker compose stack is up (make up). jq and curl must be installed.
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8080}"
KAFKA="${KAFKA_BOOTSTRAP:-localhost:9092}"
POSTGRES_HOST="${POSTGRES_HOST:-localhost}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"
POSTGRES_USER="${POSTGRES_USER:-oex}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-oex}"
POSTGRES_DB="${POSTGRES_DB:-openexchange}"

# ── Helpers ──────────────────────────────────────────────────────────────────
log() { echo "[seed] $*"; }
die() { echo "[seed] ERROR: $*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required (brew install $1)"
}

require curl
require jq

# ── Kafka topics ─────────────────────────────────────────────────────────────
log "Creating Kafka topics (idempotent — skips if they already exist)…"

# Use the Kafka CLI from the running container so we don't need it locally.
docker compose -f deploy/docker-compose.yml exec -T kafka \
  kafka-topics.sh --bootstrap-server localhost:9092 \
  --create --if-not-exists --topic orders       --partitions 4 --replication-factor 1
docker compose -f deploy/docker-compose.yml exec -T kafka \
  kafka-topics.sh --bootstrap-server localhost:9092 \
  --create --if-not-exists --topic trades       --partitions 4 --replication-factor 1
docker compose -f deploy/docker-compose.yml exec -T kafka \
  kafka-topics.sh --bootstrap-server localhost:9092 \
  --create --if-not-exists --topic risk-signals --partitions 4 --replication-factor 1

log "Kafka topics ready."

# ── Gateway JWT (stdlib only — no external tool required) ────────────────────
JWT_SECRET="${JWT_SECRET:-change-me-in-prod}"

mint_jwt() {
  local account="$1"
  python3 - "$account" "$JWT_SECRET" <<'EOF'
import base64, hashlib, hmac, json, sys, time
def b64(b): return base64.urlsafe_b64encode(b).rstrip(b"=").decode()
acct, secret = sys.argv[1], sys.argv[2]
header  = b64(json.dumps({"alg":"HS256","typ":"JWT"}).encode())
payload = b64(json.dumps({"sub": acct, "exp": int(time.time()) + 3600}).encode())
sig = b64(hmac.new(secret.encode(), f"{header}.{payload}".encode(), hashlib.sha256).digest())
print(f"{header}.{payload}.{sig}")
EOF
}

# ── Wait for the gateway to be healthy ───────────────────────────────────────
log "Waiting for gateway at $GATEWAY…"
for i in $(seq 1 30); do
  if curl -sf "$GATEWAY/healthz" >/dev/null 2>&1; then
    log "Gateway is up."
    break
  fi
  if [ "$i" -eq 30 ]; then die "Gateway did not become healthy after 30s."; fi
  sleep 1
done

# ── Place seed orders to generate some live market data ──────────────────────
log "Placing seed orders (alice buys, bob sells, crossing matches generate trades)…"

SYMBOLS=("AAPL" "TSLA" "MSFT")
ACCOUNTS=("alice" "bob" "carol")

for acct in "${ACCOUNTS[@]}"; do
  JWT=$(mint_jwt "$acct")

  for sym in "${SYMBOLS[@]}"; do
    # Alternate side per account for realistic crossing.
    if [ "$acct" = "alice" ] || [ "$acct" = "carol" ]; then
      SIDE="BUY"
    else
      SIDE="SELL"
    fi

    RESP=$(curl -sf -X POST "$GATEWAY/orders" \
      -H "Authorization: Bearer $JWT" \
      -H "Content-Type: application/json" \
      -d "{\"symbol\":\"$sym\",\"side\":\"$SIDE\",\"type\":\"LIMIT\",\"priceTicks\":15000,\"quantity\":10}" \
      2>&1) || { log "WARN: order for $acct/$sym failed (gateway may be in mock mode)"; continue; }
    STATUS=$(echo "$RESP" | jq -r '.status // "?"' 2>/dev/null || echo "?")
    log "  $acct → $sym $SIDE: $STATUS"
  done
done

log ""
log "Seed complete. Services:"
log "  Dashboard  → http://localhost:5173"
log "  Gateway    → $GATEWAY"
log "  Grafana    → http://localhost:3000  (auto-login, Viewer role)"
log "  Prometheus → http://localhost:9090"
