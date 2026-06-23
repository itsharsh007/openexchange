// Pure geometry for the market-depth chart. Kept React-free so it can be unit
// tested in isolation — the component (DepthChart.tsx) only does layout + SVG.
//
// All prices are integer ticks. "Cumulative depth" at a price is the total resting
// quantity you'd consume walking the book from the touch out to that price.

export interface Level {
  priceTicks: number;
  quantity: number;
}

export interface Pt {
  price: number; // ticks
  cum: number; // running cumulative quantity at and inside this level
}

/**
 * Running cumulative quantity over levels in the order the book sends them
 * (bids highest-first, asks lowest-first), so `cum` grows away from the touch.
 * Truncated to `depth` levels.
 */
export function cumulate(levels: Level[], depth: number): Pt[] {
  const out: Pt[] = [];
  let running = 0;
  for (const l of levels.slice(0, depth)) {
    running += l.quantity;
    out.push({ price: l.priceTicks, cum: running });
  }
  return out;
}

/**
 * Bid depth at `price` = total bid size resting at or above that price. The bid
 * points carry cum sums that grow as price falls, so the answer is the cum of the
 * lowest-priced level still at or above `price` (0 if none qualifies).
 */
export function bidDepthAt(bids: Pt[], price: number): number {
  let cum = 0;
  for (const p of bids) if (p.price >= price) cum = Math.max(cum, p.cum);
  return cum;
}

/** Ask depth at `price` = total ask size resting at or below that price. */
export function askDepthAt(asks: Pt[], price: number): number {
  let cum = 0;
  for (const p of asks) if (p.price <= price) cum = Math.max(cum, p.cum);
  return cum;
}

export interface Px {
  x: number;
  y: number;
}

/** Step-after polyline through pixel points (horizontal then vertical at each step). */
export function stepLine(px: Px[]): string {
  if (px.length === 0) return "";
  let d = `M ${px[0].x} ${px[0].y}`;
  for (let i = 1; i < px.length; i++) {
    d += ` L ${px[i].x} ${px[i - 1].y} L ${px[i].x} ${px[i].y}`;
  }
  return d;
}

/** Same staircase as {@link stepLine}, closed down to `baseY` for a filled area. */
export function stepArea(px: Px[], baseY: number): string {
  if (px.length === 0) return "";
  return (
    `M ${px[0].x} ${baseY} L ${px[0].x} ${px[0].y}` +
    px.slice(1).map((p, i) => ` L ${p.x} ${px[i].y} L ${p.x} ${p.y}`).join("") +
    ` L ${px[px.length - 1].x} ${baseY} Z`
  );
}
