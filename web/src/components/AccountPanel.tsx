import type { AccountSnapshot, TrackedOrder } from "../types";
import { cancelOrder, ApiError } from "../api/client";
import { ticksToPrice, ticksToSignedPrice } from "../util/format";
import styles from "./AccountPanel.module.css";

// Account balance / open orders / P&L.
//
// The account snapshot is mock (but typed) — real data will come from the
// gateway's account endpoints / ledger later. Open orders come from the live
// blotter the App maintains (optimistic + reconciled), so cancels and fills are
// reflected here without extra plumbing.

interface AccountPanelProps {
  account: AccountSnapshot;
  orders: TrackedOrder[];
  onReconcile: (clientOrderId: string, patch: Partial<TrackedOrder>) => void;
}

// Open = anything still working on the book.
const OPEN_STATUSES = new Set(["PENDING", "ACCEPTED", "PARTIALLY_FILLED"]);

export function AccountPanel({
  account,
  orders,
  onReconcile,
}: AccountPanelProps) {
  const openOrders = orders.filter((o) => OPEN_STATUSES.has(o.status));
  const totalPnl =
    account.realizedPnlTicks + account.unrealizedPnlTicks;

  async function handleCancel(o: TrackedOrder) {
    if (!o.orderId) return; // can't cancel something the engine hasn't acked yet
    try {
      const ack = await cancelOrder({
        orderId: o.orderId,
        symbol: o.symbol,
        accountId: account.accountId,
      });
      onReconcile(o.clientOrderId, { status: ack.status });
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "network error";
      console.error("cancel failed", msg);
    }
  }

  return (
    <section className={styles.panel}>
      <header className={styles.header}>
        <h2>Account</h2>
        <span className={styles.acct}>{account.accountId}</span>
      </header>

      <dl className={styles.stats}>
        <div>
          <dt>Cash</dt>
          <dd>{ticksToPrice(account.cashTicks)}</dd>
        </div>
        <div>
          <dt>Realized P&L</dt>
          <dd className={account.realizedPnlTicks >= 0 ? styles.up : styles.down}>
            {ticksToSignedPrice(account.realizedPnlTicks)}
          </dd>
        </div>
        <div>
          <dt>Unrealized P&L</dt>
          <dd className={account.unrealizedPnlTicks >= 0 ? styles.up : styles.down}>
            {ticksToSignedPrice(account.unrealizedPnlTicks)}
          </dd>
        </div>
        <div>
          <dt>Total P&L</dt>
          <dd className={totalPnl >= 0 ? styles.up : styles.down}>
            {ticksToSignedPrice(totalPnl)}
          </dd>
        </div>
      </dl>

      <h3 className={styles.subhead}>Positions</h3>
      <ul className={styles.positions}>
        {account.positions.map((p) => (
          <li key={p.symbol}>
            <span>{p.symbol}</span>
            <span className={p.quantity >= 0 ? styles.up : styles.down}>
              {p.quantity}
            </span>
            <span>@ {ticksToPrice(p.avgPriceTicks)}</span>
          </li>
        ))}
        {account.positions.length === 0 && (
          <li className={styles.muted}>flat</li>
        )}
      </ul>

      <h3 className={styles.subhead}>Open Orders ({openOrders.length})</h3>
      <ul className={styles.orders}>
        {openOrders.map((o) => (
          <li key={o.clientOrderId} className={styles.order}>
            <span className={o.side === "BUY" ? styles.up : styles.down}>
              {o.side}
            </span>
            <span>
              {o.quantity - o.filledQuantity}
              {o.filledQuantity > 0 ? `/${o.quantity}` : ""}
            </span>
            <span>{o.type === "MARKET" ? "MKT" : ticksToPrice(o.priceTicks)}</span>
            <span className={styles.status}>{o.status}</span>
            <button
              className={styles.cancel}
              disabled={!o.orderId}
              onClick={() => handleCancel(o)}
              title={o.orderId ? "Cancel order" : "Awaiting ack"}
            >
              ✕
            </button>
          </li>
        ))}
        {openOrders.length === 0 && (
          <li className={styles.muted}>no open orders</li>
        )}
      </ul>
    </section>
  );
}
