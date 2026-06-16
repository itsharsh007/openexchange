import type { Trade } from "../types";
import { formatTime, ticksToPrice } from "../util/format";
import styles from "./TradeTape.module.css";

// Scrolling list of recent trades (the "tape").
//
// The parent owns the trade array and caps its length (see App.tsx) so this
// component stays cheap — a long unbounded list is the classic source of jank
// in a high-frequency feed. We colour each print by aggressor side inferred
// from the previous print's price (uptick = buy-side, downtick = sell-side),
// a common tape convention.

interface TradeTapeProps {
  trades: Trade[]; // newest first
}

export function TradeTape({ trades }: TradeTapeProps) {
  return (
    <section className={styles.tape}>
      <header className={styles.header}>
        <h2>Trades</h2>
      </header>
      <div className={styles.colHead}>
        <span>Time</span>
        <span>Price</span>
        <span>Qty</span>
      </div>
      <ul className={styles.list}>
        {trades.map((t, i) => {
          // Tick direction vs. the (older) next print in the list.
          const prev = trades[i + 1];
          const dir =
            prev == null
              ? "flat"
              : t.priceTicks > prev.priceTicks
                ? "up"
                : t.priceTicks < prev.priceTicks
                  ? "down"
                  : "flat";
          return (
            <li key={t.tradeId} className={`${styles.row} ${styles[dir]}`}>
              <span>{formatTime(t.tsMillis)}</span>
              <span className={styles.price}>{ticksToPrice(t.priceTicks)}</span>
              <span className={styles.qty}>{t.quantity}</span>
            </li>
          );
        })}
        {trades.length === 0 && (
          <li className={styles.empty}>waiting for trades…</li>
        )}
      </ul>
    </section>
  );
}
