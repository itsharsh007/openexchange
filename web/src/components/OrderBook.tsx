import { useMemo } from "react";
import type { BookSnapshot, PriceLevel } from "../types";
import { ticksToPrice } from "../util/format";
import styles from "./OrderBook.module.css";

// Live order-book ladder with a simple depth visualization.
//
// The book arrives best-first (bids = highest price first, asks = lowest first,
// per the proto comment). We render asks top-down with the best ask nearest the
// spread, and bids below it — the conventional ladder layout.
//
// Depth bar: each row gets a background bar whose width is proportional to the
// cumulative quantity at that level vs. the deepest cumulative on that side.
// This is the cheap, classic "depth" cue without a full chart library.

interface OrderBookProps {
  book: BookSnapshot | null;
  /** How many levels per side to show. */
  depth?: number;
}

interface Row {
  level: PriceLevel;
  cumulative: number;
}

// Build rows with running cumulative quantity (for the depth bar denominator).
function withCumulative(levels: PriceLevel[], depth: number): Row[] {
  const out: Row[] = [];
  let running = 0;
  for (const level of levels.slice(0, depth)) {
    running += level.quantity;
    out.push({ level, cumulative: running });
  }
  return out;
}

export function OrderBook({ book, depth = 12 }: OrderBookProps) {
  // useMemo: the book updates on every WS frame; avoid recomputing slices and
  // the max when nothing relevant changed within a render.
  const { bids, asks, maxCum, spread } = useMemo(() => {
    const bidRows = withCumulative(book?.bids ?? [], depth);
    const askRows = withCumulative(book?.asks ?? [], depth);
    const maxCumulative = Math.max(
      1, // guard against /0 when the book is empty
      bidRows.at(-1)?.cumulative ?? 0,
      askRows.at(-1)?.cumulative ?? 0,
    );
    // Spread = best ask - best bid, in ticks.
    const bestBid = book?.bids[0]?.priceTicks;
    const bestAsk = book?.asks[0]?.priceTicks;
    const sp =
      bestBid != null && bestAsk != null ? bestAsk - bestBid : null;
    return { bids: bidRows, asks: askRows, maxCum: maxCumulative, spread: sp };
  }, [book, depth]);

  return (
    <section className={styles.book}>
      <header className={styles.header}>
        <h2>Order Book</h2>
        <span className={styles.symbol}>{book?.symbol ?? "—"}</span>
      </header>

      <div className={styles.colHead}>
        <span>Price</span>
        <span>Qty</span>
        <span>Total</span>
      </div>

      {/* Asks: render best (lowest) ask closest to the spread → reverse so the
          worst ask is at the top and the spread sits in the middle. */}
      <div className={styles.asks}>
        {[...asks].reverse().map((row) => (
          <Ladder key={`a-${row.level.priceTicks}`} row={row} maxCum={maxCum} side="ask" />
        ))}
      </div>

      <div className={styles.spread}>
        {spread != null ? (
          <>
            spread <strong>{ticksToPrice(spread)}</strong>
          </>
        ) : (
          <span className={styles.muted}>no spread</span>
        )}
      </div>

      <div className={styles.bids}>
        {bids.map((row) => (
          <Ladder key={`b-${row.level.priceTicks}`} row={row} maxCum={maxCum} side="bid" />
        ))}
      </div>
    </section>
  );
}

// One ladder row. The depth bar is a positioned div sized by cumulative/max.
function Ladder({
  row,
  maxCum,
  side,
}: {
  row: Row;
  maxCum: number;
  side: "bid" | "ask";
}) {
  const pct = Math.min(100, (row.cumulative / maxCum) * 100);
  return (
    <div className={`${styles.row} ${side === "bid" ? styles.bidRow : styles.askRow}`}>
      <div
        className={styles.depthBar}
        style={{ width: `${pct}%` }}
        aria-hidden
      />
      <span className={styles.price}>{ticksToPrice(row.level.priceTicks)}</span>
      <span className={styles.qty}>{row.level.quantity}</span>
      <span className={styles.total}>{row.cumulative}</span>
    </div>
  );
}
