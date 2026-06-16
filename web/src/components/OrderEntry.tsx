import { useState } from "react";
import type { NewOrder, OrderType, Side, TrackedOrder } from "../types";
import { submitOrder, ApiError } from "../api/client";
import { newClientOrderId } from "../util/id";
import { priceToTicks } from "../util/format";
import styles from "./OrderEntry.module.css";

// Order-entry form with OPTIMISTIC placement.
//
// Optimistic flow (see README "optimistic order placement"):
//  1. On submit we generate a client_order_id and immediately add a PENDING
//     TrackedOrder to local state via onOptimisticAdd — the user sees their
//     order in the blotter instantly, before the network round-trip.
//  2. We POST to the gateway. On the OrderAck we reconcile: the same
//     client_order_id row is upgraded to the engine's real orderId + status.
//  3. On failure (network or REJECTED) we roll back / mark the row REJECTED so
//     the UI never lies about what the engine actually did.

interface OrderEntryProps {
  accountId: string;
  symbol: string;
  /** Add an optimistic PENDING order to the blotter. */
  onOptimisticAdd: (order: TrackedOrder) => void;
  /** Reconcile a tracked order by clientOrderId with the engine ack/result. */
  onReconcile: (clientOrderId: string, patch: Partial<TrackedOrder>) => void;
}

export function OrderEntry({
  accountId,
  symbol,
  onOptimisticAdd,
  onReconcile,
}: OrderEntryProps) {
  const [side, setSide] = useState<Side>("BUY");
  const [type, setType] = useState<OrderType>("LIMIT");
  const [price, setPrice] = useState("100.00");
  const [quantity, setQuantity] = useState("10");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    const qty = parseInt(quantity, 10);
    if (!Number.isInteger(qty) || qty <= 0) {
      setError("Quantity must be a positive whole number.");
      return;
    }
    // MARKET orders carry no price; the engine ignores price_ticks for them.
    const priceTicks = type === "MARKET" ? 0 : priceToTicks(price);
    if (type === "LIMIT" && priceTicks <= 0) {
      setError("Limit price must be positive.");
      return;
    }

    const clientOrderId = newClientOrderId();
    const order: NewOrder = {
      clientOrderId,
      accountId,
      symbol,
      side,
      type,
      priceTicks,
      quantity: qty,
    };

    // (1) Optimistic insert — show it before we hear back.
    onOptimisticAdd({
      clientOrderId,
      symbol,
      side,
      type,
      priceTicks,
      quantity: qty,
      filledQuantity: 0,
      status: "PENDING",
      createdAt: Date.now(),
    });

    setBusy(true);
    try {
      // (2) Round-trip and reconcile with the real ack.
      const ack = await submitOrder(order);
      onReconcile(clientOrderId, {
        orderId: ack.orderId,
        status: ack.status,
        filledQuantity: ack.filledQuantity,
      });
      if (ack.status === "REJECTED") {
        setError(`Rejected: ${ack.reason || "unknown reason"}`);
      }
    } catch (err) {
      // (3) Roll back to REJECTED on transport failure so the UI is honest.
      const msg = err instanceof ApiError ? err.message : "network error";
      onReconcile(clientOrderId, { status: "REJECTED" });
      setError(`Submit failed: ${msg}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className={styles.entry}>
      <header className={styles.header}>
        <h2>Order Entry</h2>
        <span className={styles.symbol}>{symbol}</span>
      </header>

      <form onSubmit={handleSubmit} className={styles.form}>
        {/* BUY / SELL toggle */}
        <div className={styles.sideToggle}>
          <button
            type="button"
            className={`${styles.sideBtn} ${side === "BUY" ? styles.buyActive : ""}`}
            onClick={() => setSide("BUY")}
          >
            BUY
          </button>
          <button
            type="button"
            className={`${styles.sideBtn} ${side === "SELL" ? styles.sellActive : ""}`}
            onClick={() => setSide("SELL")}
          >
            SELL
          </button>
        </div>

        <label className={styles.field}>
          <span>Type</span>
          <select
            value={type}
            onChange={(e) => setType(e.target.value as OrderType)}
          >
            <option value="LIMIT">LIMIT</option>
            <option value="MARKET">MARKET</option>
          </select>
        </label>

        <label className={styles.field}>
          <span>Price</span>
          <input
            type="number"
            step="0.01"
            min="0"
            value={price}
            disabled={type === "MARKET"}
            onChange={(e) => setPrice(e.target.value)}
          />
        </label>

        <label className={styles.field}>
          <span>Quantity</span>
          <input
            type="number"
            step="1"
            min="1"
            value={quantity}
            onChange={(e) => setQuantity(e.target.value)}
          />
        </label>

        <button
          type="submit"
          disabled={busy}
          className={`${styles.submit} ${side === "BUY" ? styles.submitBuy : styles.submitSell}`}
        >
          {busy ? "Submitting…" : `${side} ${quantity} ${symbol}`}
        </button>

        {error && <p className={styles.error}>{error}</p>}
      </form>
    </section>
  );
}
