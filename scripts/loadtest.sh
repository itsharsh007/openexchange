#!/usr/bin/env bash
# Load test the gateway: read path (GET /book) + write path (POST /orders).
# Records throughput + latency percentiles for the README. Uses `hey`.
#
#   ./scripts/loadtest.sh            # defaults: 20s, 50 concurrent
#   GATEWAY=... DURATION=30s C=100 ./scripts/loadtest.sh
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8080}"
DURATION="${DURATION:-20s}"
C="${C:-50}"
JWT_SECRET="${JWT_SECRET:-change-me-in-prod}"

# Mint a short-lived HS256 JWT (stdlib only).
JWT=$(python3 - "loadtester" "$JWT_SECRET" <<'EOF'
import base64, hashlib, hmac, json, sys, time
def b64(b): return base64.urlsafe_b64encode(b).rstrip(b"=").decode()
acct, secret = sys.argv[1], sys.argv[2]
h = b64(json.dumps({"alg":"HS256","typ":"JWT"}).encode())
p = b64(json.dumps({"sub": acct, "exp": int(time.time())+3600}).encode())
s = b64(hmac.new(secret.encode(), f"{h}.{p}".encode(), hashlib.sha256).digest())
print(f"{h}.{p}.{s}")
EOF
)

command -v hey >/dev/null 2>&1 || { echo "installing hey..."; go install github.com/rakyll/hey@latest; export PATH="$PATH:$(go env GOPATH)/bin"; }
export PATH="$PATH:$(go env GOPATH)/bin"

echo "=================================================================="
echo " READ PATH — GET /book/AAPL  (c=$C, $DURATION)"
echo "=================================================================="
hey -z "$DURATION" -c "$C" -H "Authorization: Bearer $JWT" \
  "$GATEWAY/book/AAPL?depth=10" | grep -A20 "Summary:"

echo "=================================================================="
echo " WRITE PATH — POST /orders  (c=$C, $DURATION)"
echo "=================================================================="
hey -z "$DURATION" -c "$C" -m POST \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"symbol":"AAPL","side":"BUY","type":"LIMIT","priceTicks":15000,"quantity":1}' \
  "$GATEWAY/orders" | grep -A20 "Summary:"
