import { useMemo, useState } from "react";
import type { BookSnapshot } from "../types";
import { ticksToPrice } from "../util/format";
import { askDepthAt, bidDepthAt, cumulate, stepArea, stepLine } from "../util/depth";
import { useElementWidth } from "../hooks/useElementWidth";
import styles from "./DepthChart.module.css";

// Cumulative market-depth chart, hand-rolled in SVG (no chart library — keeps the
// bundle lean and the math fully under our control).
//
// The classic depth view: cumulative bid liquidity rises as price falls (green,
// left of the mid) and cumulative ask liquidity rises as price climbs (red, right
// of the mid). The empty wedge in the middle is the spread. Reading it: the height
// of a curve at price p is "how much size could I fill walking the book to p".
//
// We measure the container width with a ResizeObserver and draw at 1 SVG unit = 1
// px so hover math is exact (no viewBox letterboxing to undo). Height is fixed.

const HEIGHT = 170;
const M = { top: 12, right: 12, bottom: 20, left: 10 };

interface DepthChartProps {
  book: BookSnapshot | null;
  /** How many price levels per side to fold into the curve. */
  depth?: number;
}

export function DepthChart({ book, depth = 20 }: DepthChartProps) {
  const [ref, width] = useElementWidth();
  const [hoverX, setHoverX] = useState<number | null>(null);

  const geom = useMemo(() => {
    if (width <= 0) return null;
    const bids = cumulate(book?.bids ?? [], depth);
    const asks = cumulate(book?.asks ?? [], depth);
    if (bids.length === 0 && asks.length === 0) return null;

    const maxCum = Math.max(1, bids.at(-1)?.cum ?? 0, asks.at(-1)?.cum ?? 0);
    const prices = [...bids, ...asks].map((p) => p.price);
    const xMin = Math.min(...prices);
    const xMax = Math.max(...prices);
    const xSpan = xMax - xMin || 1; // guard a one-sided / single-level book

    const plotW = width - M.left - M.right;
    const plotH = HEIGHT - M.top - M.bottom;
    const baseY = M.top + plotH;
    const xScale = (price: number) => M.left + ((price - xMin) / xSpan) * plotW;
    const yScale = (cum: number) => baseY - (cum / maxCum) * plotH;

    // Step-after staircase + a baseline-closed area for the fill. Bids are sorted
    // ascending in price so the curve descends toward the mid; asks already are.
    const toPx = (pts: typeof bids) => pts.map((p) => ({ x: xScale(p.price), y: yScale(p.cum) }));
    const bidPx = toPx([...bids].sort((a, b) => a.price - b.price));
    const askPx = toPx(asks);

    const bestBid = book?.bids[0]?.priceTicks;
    const bestAsk = book?.asks[0]?.priceTicks;
    const mid = bestBid != null && bestAsk != null ? (bestBid + bestAsk) / 2 : null;

    return {
      bids, asks, maxCum, xMin, xMax, xSpan, plotW, plotH, baseY, mid,
      xScale,
      bidArea: stepArea(bidPx, baseY), bidLine: stepLine(bidPx),
      askArea: stepArea(askPx, baseY), askLine: stepLine(askPx),
      priceAt: (x: number) => xMin + ((x - M.left) / plotW) * xSpan,
    };
  }, [book, depth, width]);

  // Resolve the hovered pixel to a price and the cumulative depth on the side it
  // falls on (bid depth = size resting at or above that price; ask = at or below).
  const hover = useMemo(() => {
    if (!geom || hoverX == null) return null;
    const price = geom.priceAt(hoverX);
    if (price < geom.xMin || price > geom.xMax) return null;
    const onBidSide = geom.mid == null ? price <= geom.xMin + geom.xSpan / 2 : price <= geom.mid;
    const cum = onBidSide ? bidDepthAt(geom.bids, price) : askDepthAt(geom.asks, price);
    return { x: hoverX, price, cum, side: onBidSide ? "bid" : "ask" as "bid" | "ask" };
  }, [geom, hoverX]);

  return (
    <section className={styles.wrap} ref={ref}>
      <header className={styles.header}>
        <h2>Market Depth</h2>
        {geom && <span className={styles.scale}>peak {geom.maxCum}</span>}
      </header>

      {!geom ? (
        <div className={styles.empty}>waiting for book…</div>
      ) : (
        <div className={styles.canvas}>
          <svg
            width={width}
            height={HEIGHT}
            className={styles.svg}
            onMouseMove={(e) => {
              const r = e.currentTarget.getBoundingClientRect();
              setHoverX(e.clientX - r.left);
            }}
            onMouseLeave={() => setHoverX(null)}
          >
            <path className={styles.bidArea} d={geom.bidArea} />
            <path className={styles.askArea} d={geom.askArea} />
            <path className={styles.bidLine} d={geom.bidLine} />
            <path className={styles.askLine} d={geom.askLine} />

            {/* Mid-price marker sits in the spread wedge. */}
            {geom.mid != null && (
              <line
                className={styles.mid}
                x1={geom.xScale(geom.mid)} x2={geom.xScale(geom.mid)}
                y1={M.top} y2={geom.baseY}
              />
            )}

            {/* Hover crosshair. */}
            {hover && (
              <line className={styles.guide} x1={hover.x} x2={hover.x} y1={M.top} y2={geom.baseY} />
            )}

            {/* X-axis price ticks. */}
            <text className={styles.label} x={M.left} y={HEIGHT - 6} textAnchor="start">
              {ticksToPrice(geom.xMin)}
            </text>
            {geom.mid != null && (
              <text className={styles.label} x={geom.xScale(geom.mid)} y={HEIGHT - 6} textAnchor="middle">
                {ticksToPrice(geom.mid)}
              </text>
            )}
            <text className={styles.label} x={width - M.right} y={HEIGHT - 6} textAnchor="end">
              {ticksToPrice(geom.xMax)}
            </text>
          </svg>

          {hover && (
            <div
              className={styles.tooltip}
              style={{
                left: Math.min(Math.max(hover.x, 4), width - 96),
                color: hover.side === "bid" ? "var(--up)" : "var(--down)",
              }}
            >
              <strong>{ticksToPrice(hover.price)}</strong>
              <span>{hover.side === "bid" ? "bid" : "ask"} depth {hover.cum}</span>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
