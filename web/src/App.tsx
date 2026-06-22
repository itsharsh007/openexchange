import { useCallback, useEffect, useRef, useState } from "react";
import { useWebSocket } from "./hooks/useWebSocket";
import { getBook, getAccount } from "./api/client";
import { ensureSession, demoAccountId } from "./api/session";
import { OrderBook } from "./components/OrderBook";
import { TradeTape } from "./components/TradeTape";
import { OrderEntry } from "./components/OrderEntry";
import { AccountPanel } from "./components/AccountPanel";
import { RiskPanel } from "./components/RiskPanel";
import type {
  AccountSnapshot,
  BookSnapshot,
  ConnectionState,
  RiskSignal,
  ServerMessage,
  Trade,
  TrackedOrder,
  WsChannel,
} from "./types";
import styles from "./App.module.css";

// Demo constants. In a real build the symbol comes from a selector and the
// account from the authenticated session. AAPL is one of the seeded symbols the
// gateway serves a book for (see scripts/seed.sh).
const SYMBOL = "AAPL";
const ACCOUNT_ID = "acct-demo-1";
const CHANNELS: WsChannel[] = ["book", "trades", "risk"];

// Cap the trade tape so the live feed never grows unbounded (jank prevention).
const MAX_TRADES = 100;

// Typed mock account — replaced by real ledger data later.
const MOCK_ACCOUNT: AccountSnapshot = {
  accountId: ACCOUNT_ID,
  cashTicks: 1_000_000, // $10,000.00
  realizedPnlTicks: 0,
  unrealizedPnlTicks: 0,
  positions: [],
};

export default function App() {
  const [book, setBook] = useState<BookSnapshot | null>(null);
  const [trades, setTrades] = useState<Trade[]>([]);
  const [risk, setRisk] = useState<RiskSignal | null>(null);
  const [orders, setOrders] = useState<TrackedOrder[]>([]);
  // Gate REST/WS until we hold a bearer token. The public dashboard fetches an
  // anonymous demo session from the gateway; locally a build-time token may exist.
  const [authReady, setAuthReady] = useState(false);
  // The account this browser trades as — unique per session, so two visitors are
  // distinct traders whose orders cross. Falls back to the constant until ready.
  const [accountId, setAccountId] = useState(ACCOUNT_ID);
  // Live cash / P&L / positions, fetched from the gateway and refreshed on fills.
  const [account, setAccount] = useState<AccountSnapshot>(MOCK_ACCOUNT);

  // Pull the latest account snapshot from the gateway. Best-effort: a failure
  // leaves the last-known values rather than blanking the panel.
  const refetchAccount = useCallback(() => {
    getAccount().then(setAccount).catch(() => {});
  }, []);

  // ── Blotter mutation helpers (optimistic add + reconcile) ──────────────────
  const addOptimistic = useCallback((order: TrackedOrder) => {
    setOrders((prev) => [order, ...prev]);
  }, []);

  const reconcile = useCallback(
    (clientOrderId: string, patch: Partial<TrackedOrder>) => {
      setOrders((prev) =>
        prev.map((o) =>
          o.clientOrderId === clientOrderId ? { ...o, ...patch } : o,
        ),
      );
    },
    [],
  );

  // ── WebSocket message handling ─────────────────────────────────────────────
  // One handler narrows the discriminated union and routes to the right slice.
  // Kept stable via useCallback so the WS effect doesn't churn.
  const handleMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case "book":
        // Full snapshot replaces the book each frame (gateway sends top-N).
        setBook(msg.data);
        break;
      case "trade":
        setTrades((prev) => [msg.data, ...prev].slice(0, MAX_TRADES));
        // A trade may have touched this account (as taker OR resting maker), so
        // refresh cash/P&L/positions. Cheap at demo volume; always correct.
        refetchAccount();
        break;
      case "risk":
        setRisk(msg.data);
        break;
      case "ack":
        // The engine can also push async acks/fills over the WS (e.g. a resting
        // order later fills). Reconcile the blotter by engine orderId.
        setOrders((prev) =>
          prev.map((o) =>
            o.orderId === msg.data.orderId
              ? {
                  ...o,
                  status: msg.data.status,
                  filledQuantity: msg.data.filledQuantity,
                }
              : o,
          ),
        );
        refetchAccount();
        break;
    }
  }, [refetchAccount]);

  const { connectionState } = useWebSocket({
    channels: CHANNELS,
    symbol: SYMBOL,
    onMessage: handleMessage,
    enabled: authReady,
  });

  // ── Obtain a bearer token before any authenticated call. Runs once on mount.
  useEffect(() => {
    ensureSession()
      .then(() => {
        setAccountId(demoAccountId() || ACCOUNT_ID);
        setAuthReady(true);
        refetchAccount();
      })
      .catch((err) => console.error("[auth] could not obtain demo session", err));
  }, [refetchAccount]);

  // ── Seed the book once via REST so the ladder isn't empty before first WS
  // frame. WHY: WS gives deltas/snapshots going forward, but on initial load we
  // want immediate state. Guarded so it runs once.
  const seeded = useRef(false);
  useEffect(() => {
    if (!authReady || seeded.current) return; // need a token before the REST call
    seeded.current = true;
    getBook(SYMBOL).then(setBook).catch(() => {
      // Gateway may be down during dev; the WS will fill the book when it's up.
    });
  }, [authReady]);

  return (
    <div className={styles.app}>
      <header className={styles.topbar}>
        <h1>
          OpenExchange <span className={styles.tag}>simulation</span>
        </h1>
        <ConnBadge state={connectionState} />
      </header>

      <main className={styles.grid}>
        <div className={`${styles.card} ${styles.bookCard}`}>
          <OrderBook book={book} />
        </div>
        <div className={`${styles.card} ${styles.tapeCard}`}>
          <TradeTape trades={trades} />
        </div>
        <div className={styles.card}>
          <OrderEntry
            accountId={accountId}
            symbol={SYMBOL}
            onOptimisticAdd={addOptimistic}
            onReconcile={reconcile}
          />
        </div>
        <div className={styles.card}>
          <AccountPanel
            account={account}
            orders={orders}
            onReconcile={reconcile}
          />
        </div>
        <div className={styles.card}>
          <RiskPanel signal={risk} />
        </div>
      </main>
    </div>
  );
}

// Connection health indicator in the top bar.
function ConnBadge({ state }: { state: ConnectionState }) {
  const label: Record<ConnectionState, string> = {
    connecting: "connecting…",
    open: "live",
    reconnecting: "reconnecting…",
    closed: "disconnected",
  };
  return (
    <span className={`${styles.conn} ${styles[state]}`}>
      <span className={styles.dot} />
      {label[state]}
    </span>
  );
}
