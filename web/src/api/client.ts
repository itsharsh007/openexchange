import { API_BASE } from "../config";
import type {
  BookSnapshot,
  CancelOrder,
  NewOrder,
  OrderAck,
} from "../types";

// Typed REST client for the Go gateway.
//
// Endpoints (per docs/architecture.md — REST for submit/cancel/book snapshot):
//   POST   /orders        body NewOrder      -> OrderAck
//   DELETE /orders/:id     body CancelOrder   -> OrderAck
//   GET    /book?symbol=&depth=               -> BookSnapshot
//
// WHY a thin wrapper instead of calling fetch inline:
//  - one place to set base URL, headers, and (later) the JWT auth token;
//  - one place to translate non-2xx responses into typed errors so callers can
//    `try/catch` instead of forgetting to check `res.ok`.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

// A single fetch wrapper that parses JSON and throws ApiError on failure.
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      // TODO: attach `Authorization: Bearer <jwt>` once auth lands on the gateway.
      ...(init?.headers ?? {}),
    },
  });

  if (!res.ok) {
    // Try to surface the gateway's error body; fall back to status text.
    let detail = res.statusText;
    try {
      const body = (await res.json()) as { reason?: string; error?: string };
      detail = body.reason ?? body.error ?? detail;
    } catch {
      /* non-JSON error body; keep statusText */
    }
    throw new ApiError(res.status, detail);
  }

  // 204 No Content has no body.
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Submit a new order. Returns the engine's ack (status + filled qty). */
export function submitOrder(order: NewOrder): Promise<OrderAck> {
  return request<OrderAck>("/orders", {
    method: "POST",
    body: JSON.stringify(order),
  });
}

/** Cancel a resting order by engine order id. */
export function cancelOrder(cancel: CancelOrder): Promise<OrderAck> {
  return request<OrderAck>(`/orders/${encodeURIComponent(cancel.orderId)}`, {
    method: "DELETE",
    body: JSON.stringify(cancel),
  });
}

/** Fetch a one-shot order-book snapshot (used to seed the ladder before WS). */
export function getBook(symbol: string, depth = 20): Promise<BookSnapshot> {
  const qs = new URLSearchParams({ symbol, depth: String(depth) });
  return request<BookSnapshot>(`/book?${qs.toString()}`);
}
