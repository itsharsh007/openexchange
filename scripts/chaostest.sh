#!/usr/bin/env bash
# Resilience test: kill the engine mid-flight and prove the gateway degrades
# gracefully (stays up, returns a clean 5xx, ledger uncorrupted), then recovers
# when the engine comes back. Plan verification #7.
#
#   ./scripts/chaostest.sh
set -uo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml"
GATEWAY="http://localhost:8080"
JWT_SECRET="${JWT_SECRET:-change-me-in-prod}"

JWT=$(python3 - "chaos" "$JWT_SECRET" <<'EOF'
import base64, hashlib, hmac, json, sys, time
def b64(b): return base64.urlsafe_b64encode(b).rstrip(b"=").decode()
acct, secret = sys.argv[1], sys.argv[2]
h = b64(json.dumps({"alg":"HS256","typ":"JWT"}).encode())
p = b64(json.dumps({"sub": acct, "exp": int(time.time())+3600}).encode())
s = b64(hmac.new(secret.encode(), f"{h}.{p}".encode(), hashlib.sha256).digest())
print(f"{h}.{p}.{s}")
EOF
)

order() {
  curl -s -o /dev/null -w "%{http_code}" -X POST "$GATEWAY/orders" \
    -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
    -d '{"symbol":"AAPL","side":"BUY","type":"LIMIT","priceTicks":15000,"quantity":1}'
}
ledger_count() {
  docker exec openexchange-postgres-1 psql -U oex -d openexchange -tAc \
    "SELECT count(*) FROM ledger_entries;" 2>/dev/null | tr -d '[:space:]'
}
imbalance() {
  docker exec openexchange-postgres-1 psql -U oex -d openexchange -tAc \
    "SELECT count(*) FROM (SELECT asset FROM ledger_entries GROUP BY asset HAVING SUM(delta)<>0) x;" 2>/dev/null | tr -d '[:space:]'
}

echo "=== BASELINE ==="
echo "order while healthy        -> HTTP $(order)"
echo "gateway /healthz           -> HTTP $(curl -s -o /dev/null -w '%{http_code}' $GATEWAY/healthz)"
L0=$(ledger_count); echo "ledger rows                -> $L0  (imbalanced assets: $(imbalance))"

echo
echo "=== CHAOS: kill the engine ==="
$COMPOSE kill engine >/dev/null 2>&1; echo "engine killed"
sleep 2
echo "order with engine DOWN     -> HTTP $(order)   (expect 502/5xx, not a crash)"
echo "gateway /healthz STILL up  -> HTTP $(curl -s -o /dev/null -w '%{http_code}' $GATEWAY/healthz)   (expect 200 — degraded, not dead)"
L1=$(ledger_count); echo "ledger rows unchanged      -> $L1  (was $L0; imbalanced assets: $(imbalance))"

echo
echo "=== RECOVERY: restart the engine ==="
$COMPOSE up -d engine >/dev/null 2>&1
for i in $(seq 1 30); do
  st=$(docker inspect -f '{{.State.Health.Status}}' openexchange-engine-1 2>/dev/null)
  [ "$st" = "healthy" ] && break
  sleep 2
done
echo "engine health             -> $(docker inspect -f '{{.State.Health.Status}}' openexchange-engine-1 2>/dev/null)"
echo "order after recovery      -> HTTP $(order)   (expect 201 — back to normal)"
echo "final imbalanced assets   -> $(imbalance)   (expect 0 — ledger never corrupted)"
