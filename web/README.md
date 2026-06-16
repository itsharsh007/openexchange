# OpenExchange — Web Dashboard (`web/`)

A real-time trading dashboard built with **React 18 + TypeScript + Vite**. It talks to the Go
gateway over **REST** (submit/cancel orders, fetch a book snapshot) and a **WebSocket** (`/ws`) for
the live trade tape, order-book updates, and ML/risk signals.

> Simulation only — play money, fake symbols. See the root `README.md`.

---

## What it does

A single-screen trading terminal:

| Panel | Component | Purpose |
|---|---|---|
| Order book | `OrderBook.tsx` | Live bids/asks ladder with a cumulative-depth bar and live spread. |
| Trade tape | `TradeTape.tsx` | Scrolling list of recent trades, coloured by up/down tick. |
| Order entry | `OrderEntry.tsx` | BUY/SELL × LIMIT/MARKET form with **optimistic** placement. |
| Account | `AccountPanel.tsx` | Cash, P&L, positions, and a live open-orders blotter with cancel. |
| Risk / ML | `RiskPanel.tsx` | Price prediction, anomaly/fraud score, account risk — from the feed. |

Connection health (`connecting / live / reconnecting / disconnected`) shows in the top bar.

---

## Project layout

```
web/
├── index.html              # Vite entry HTML
├── vite.config.ts          # Vite + React plugin config
├── tsconfig.json           # strict TypeScript, bundler module resolution
├── Dockerfile              # multi-stage: build → nginx serve
├── nginx.conf              # SPA fallback + asset caching
├── .env.example            # VITE_API_BASE / VITE_WS_URL
└── src/
    ├── main.tsx            # React 18 root
    ├── App.tsx             # layout + state wiring (book/trades/risk/orders)
    ├── config.ts           # env vars + tick→price convention
    ├── types.ts            # TS mirror of proto/openexchange.proto + WS envelope
    ├── api/client.ts       # typed REST client
    ├── hooks/useWebSocket.ts  # reconnecting, typed WS hook
    ├── util/format.ts      # ticks↔price, time formatting
    ├── util/id.ts          # client_order_id (idempotency key) generator
    └── components/*.tsx    # the five panels (+ scoped .module.css)
```

---

## The tick → price convention

Prices on the wire are **integer ticks**, never floats — this mirrors the proto
(`price_ticks`) and the engine, and avoids floating-point money bugs (`0.1 + 0.2 !== 0.3`).

- **1 tick = 1 cent**, so `human price = price_ticks / 100` (`TICKS_PER_UNIT` in `src/config.ts`).
- All arithmetic (spread, depth, P&L) stays in integer ticks.
- Conversion to a human string happens at the very last step, in `util/format.ts`
  (`ticksToPrice`, `priceToTicks`). The order form parses the typed price back into ticks with
  `Math.round(price * 100)` to avoid float dust.
- **Quantities are whole units** (shares/contracts) and are NOT scaled.

---

## How the WebSocket reconnect / backoff works (`src/hooks/useWebSocket.ts`)

A real-time UI must survive gateway restarts and flaky networks without a tab refresh.

- **Typed messages.** Frames are JSON `{ "type": ..., "data": ... }`. `ServerMessage` is a
  discriminated union (`trade | book | risk | ack`) so the handler narrows `data` by `type` with
  full type safety. A malformed frame is logged and skipped — it never kills the feed.
- **Exponential backoff + jitter.** On every unexpected close we retry after
  `min(base · 2^attempt, max)` ms (`base = 500ms`, `max = 30s`), **plus** up to 30% random jitter.
  The exponent is capped so `2^n` can't overflow. Jitter spreads reconnects so a fleet of browsers
  doesn't stampede the gateway after a blip (thundering-herd avoidance). The backoff counter resets
  to 0 on a healthy `onopen`.
- **Subscription survival.** The hook remembers the desired `channels` + `symbol` and re-sends the
  `subscribe` frame on **every** (re)connect, so server-side subscription state is restored after a
  drop without app-level coordination. Changing the subscription sends a new `subscribe` on the
  existing socket — it does **not** tear down the connection.
- **No re-render churn.** The socket, timers, and latest callbacks live in `useRef`; only *data*
  goes into React state. Re-renders therefore never recreate the socket. Cleanup on unmount stops
  the reconnect loop and closes the socket (safe under React 18 StrictMode double-mount).

---

## How optimistic order placement works (`OrderEntry.tsx` + `App.tsx`)

The user should see their order instantly, before the network round-trip, then have the UI
reconcile to the truth.

1. **Generate an idempotency key.** `newClientOrderId()` creates a `client_order_id` (the proto's
   client-supplied idempotency key). The engine dedupes on it, so a retry never double-submits.
2. **Optimistic insert.** A `TrackedOrder` with status `PENDING` is added to the blotter
   immediately (`onOptimisticAdd`).
3. **Round-trip.** We `POST /orders`. On the `OrderAck` we **reconcile** the same row (matched by
   `clientOrderId`) with the engine's real `orderId`, `status`, and `filledQuantity`.
4. **Failure / rejection.** On a transport error or `status === "REJECTED"` we mark the row
   `REJECTED` and surface the reason — the UI never claims success the engine didn't grant.
5. **Async fills.** A resting order can fill later; the gateway can push an `ack` frame over the WS,
   which `App.tsx` reconciles by `orderId`. Cancels follow the same reconcile path.

---

## Rendering a high-frequency feed without jank

- The trade tape is **capped** (`MAX_TRADES = 100`) so the list never grows unbounded.
- Book updates are full top-N snapshots that replace state; derived values (cumulative depth, max,
  spread) are computed in `useMemo`.
- `tabular-nums` keeps digits from shifting width as prices tick.
- Message routing is a single `useCallback`'d handler, so the WS effect stays stable.

---

## State management choice

Plain **React hooks** (`useState` + `useRef` + `useCallback`) — no Redux/Zustand. The state is
small, local to one screen, and mostly fed by one WebSocket. The blotter's optimistic-add /
reconcile pattern is a couple of pure state updaters lifted into `App.tsx`. A global store would add
ceremony without payoff here; if the app grew multiple routes sharing server state, a fetching/cache
library (e.g. TanStack Query) would be the next step rather than a hand-rolled store.

---

## Configuration (env vars)

Set via Vite env vars (Twelve-Factor — config in the environment, never hardcoded). Copy
`.env.example` to `.env.local`:

| Var | Default | Meaning |
|---|---|---|
| `VITE_API_BASE` | `http://localhost:8080` | REST base URL (gateway). |
| `VITE_WS_URL`   | `ws://localhost:8080/ws` | Live-feed WebSocket URL. |

Only `VITE_`-prefixed vars are exposed to client code. They are **build-time** inlined by Vite — to
change them for a Docker image, pass `--build-arg VITE_API_BASE=...` (see `Dockerfile`).

---

## Run

```bash
cd web
npm install
npm run dev        # http://localhost:5173

# verify it compiles + bundles:
npm run build      # runs `tsc --noEmit` then `vite build`
npm run typecheck  # type-check only
```

### Docker

```bash
docker build -t openexchange-web \
  --build-arg VITE_API_BASE=https://api.example \
  --build-arg VITE_WS_URL=wss://api.example/ws .
docker run -p 8080:80 openexchange-web
```

---

## Contract notes / TODOs

- The gateway's exact REST routes (`POST /orders`, `DELETE /orders/:id`, `GET /book`) and the WS
  envelope (`{type,data}`) are **this dashboard's assumed contract** — the gateway service isn't
  implemented yet. Align both sides when it lands.
- **`RiskSignal` is not yet in `proto/openexchange.proto`.** It's defined in `src/types.ts` as the
  expected shape from the `risk-signals` topic. Add a proto message and mirror it here when ready.
- The account snapshot is **typed mock data** (`MOCK_ACCOUNT` in `App.tsx`); wire it to the ledger
  endpoints later.
- No unit tests yet (`npm test` is a stub).
