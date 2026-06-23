import { useMemo } from "react";
import type { Trade } from "../types";
import { ticksToPrice, ticksToSignedPrice } from "../util/format";
import { useElementWidth } from "../hooks/useElementWidth";
import { areaPath, linePath, scale } from "../util/sparkline";
import styles from "./PriceChart.module.css";

// Streaming last-trade price line. Trades arrive newest-first; we keep the most
// recent MAX prints, plot them oldest→newest, and auto-scale Y to the window so
// small moves stay legible. Colour tracks the net move across the window.

const HEIGHT = 88;
const MAX = 60; // points held in the rolling window

export function PriceChart({ trades }: { trades: Trade[] }) {
  const [ref, width] = useElementWidth();

  const view = useMemo(() => {
    const prices = trades.slice(0, MAX).reverse().map((t) => t.priceTicks);
    if (width <= 0 || prices.length === 0) return null;
    const s = scale(prices, width, HEIGHT);
    const last = prices[prices.length - 1];
    const change = last - prices[0];
    return {
      line: linePath(prices, s),
      area: areaPath(prices, s, HEIGHT),
      last,
      change,
      up: change >= 0,
      dotX: s.x(prices.length - 1),
      dotY: s.y(last),
    };
  }, [trades, width]);

  return (
    <section className={styles.wrap} ref={ref}>
      <header className={styles.header}>
        <h2>Last Price</h2>
        {view && (
          <span className={view.up ? styles.up : styles.down}>
            {ticksToPrice(view.last)} <small>{ticksToSignedPrice(view.change)}</small>
          </span>
        )}
      </header>

      {!view ? (
        <div className={styles.empty}>waiting for trades…</div>
      ) : (
        <svg width={width} height={HEIGHT} className={styles.svg}>
          <path className={view.up ? styles.areaUp : styles.areaDown} d={view.area} />
          <path className={view.up ? styles.lineUp : styles.lineDown} d={view.line} />
          <circle
            cx={view.dotX}
            cy={view.dotY}
            r="2.5"
            className={view.up ? styles.dotUp : styles.dotDown}
          />
        </svg>
      )}
    </section>
  );
}
