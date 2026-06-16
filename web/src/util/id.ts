// Generate a client-side idempotency key for orders.
//
// WHY: the proto's NewOrder carries a `client_order_id` the *client* supplies so
// the engine can dedupe re-delivered submits (network retries never create two
// orders). We must generate it before sending, which also lets the UI place the
// order optimistically and later reconcile the ack by this same id.
export function newClientOrderId(): string {
  // crypto.randomUUID is available in all modern browsers and Node 16+.
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  // Fallback for ancient environments.
  return `cid-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
