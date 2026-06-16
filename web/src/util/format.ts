import { TICKS_PER_UNIT } from "../config";

// Formatting helpers. All money math stays in integer ticks; these are the ONLY
// place we cross over to a human-readable string, at the last possible moment.

/** ticks → "123.45" string. 1 tick = 1 cent (TICKS_PER_UNIT = 100). */
export function ticksToPrice(ticks: number): string {
  return (ticks / TICKS_PER_UNIT).toFixed(2);
}

/** "123.45" (or 123.45) → integer ticks. Used when reading the order form. */
export function priceToTicks(price: string | number): number {
  const n = typeof price === "string" ? parseFloat(price) : price;
  if (!Number.isFinite(n)) return 0;
  // Round to nearest tick to avoid float dust (e.g. 0.1 * 100 = 10.000000001).
  return Math.round(n * TICKS_PER_UNIT);
}

/** Signed P&L in ticks → "+12.30" / "-4.00" for display. */
export function ticksToSignedPrice(ticks: number): string {
  const sign = ticks >= 0 ? "+" : "-";
  return sign + ticksToPrice(Math.abs(ticks));
}

/** HH:MM:SS from epoch millis, for the trade tape. */
export function formatTime(tsMillis: number): string {
  const d = new Date(tsMillis);
  return d.toLocaleTimeString(undefined, { hour12: false });
}
