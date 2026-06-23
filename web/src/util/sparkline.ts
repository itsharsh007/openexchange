// Pure helpers for the streaming price sparkline. React-free so the geometry is
// unit-testable; the component (PriceChart.tsx) only does layout + SVG.
//
// Values are arbitrary numbers (here: last-trade prices in ticks, oldest→newest).
// We auto-scale Y to the visible window's min/max so small moves stay legible.

export interface Scale {
  /** pixel x for sample index i */
  x: (i: number) => number;
  /** pixel y for value v */
  y: (v: number) => number;
  min: number;
  max: number;
}

/** Build the index→x / value→y mapping for `n` samples in a w×h box with padding. */
export function scale(values: number[], w: number, h: number, pad = 3): Scale {
  const min = values.length ? Math.min(...values) : 0;
  const max = values.length ? Math.max(...values) : 0;
  const span = max - min || 1; // flat series → don't divide by zero
  const n = values.length;
  return {
    min,
    max,
    x: (i) => (n <= 1 ? w / 2 : pad + (i / (n - 1)) * (w - 2 * pad)),
    y: (v) => pad + (1 - (v - min) / span) * (h - 2 * pad),
  };
}

/** Polyline path through the samples. */
export function linePath(values: number[], s: Scale): string {
  if (values.length === 0) return "";
  return values
    .map((v, i) => `${i === 0 ? "M" : "L"} ${s.x(i).toFixed(2)} ${s.y(v).toFixed(2)}`)
    .join(" ");
}

/** Same line, closed down to the baseline for a soft area fill under the curve. */
export function areaPath(values: number[], s: Scale, h: number, pad = 3): string {
  if (values.length === 0) return "";
  const baseY = h - pad;
  const firstX = s.x(0);
  const lastX = s.x(values.length - 1);
  return `${linePath(values, s)} L ${lastX.toFixed(2)} ${baseY} L ${firstX.toFixed(2)} ${baseY} Z`;
}
