import { API_BASE } from "../config";
import { authToken, refreshAccess } from "./session";
import type {
  AccountSnapshot,
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
// On a 401 it transparently tries to refresh the access token once, then retries
// — so a logged-in user whose short access token expired mid-session doesn't see
// an error (the rotating refresh token mints a new one behind the scenes).
async function request<T>(path: string, init?: RequestInit, retry = true): Promise<T> {
  const tok = authToken();
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
      ...(init?.headers ?? {}),
    },
  });

  if (res.status === 401 && retry && (await refreshAccess())) {
    return request<T>(path, init, false);
  }

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
  return request<BookSnapshot>(`/book/${encodeURIComponent(symbol)}?depth=${depth}`);
}

/** Fetch the authenticated account's cash, P&L, and positions. */
export function getAccount(): Promise<AccountSnapshot> {
  return request<AccountSnapshot>("/account");
}

/** Market-simulator state. `enabled` is false (404) when no bots run (full stack). */
export interface SimState {
  enabled: boolean;
  paused: boolean;
}

/** Whether the bot market simulator is running and whether it's paused. */
export async function getSimState(): Promise<SimState> {
  try {
    return await request<SimState>("/sim/state");
  } catch {
    return { enabled: false, paused: false }; // route absent → no sim on this gateway
  }
}

/** Pause (true) or resume (false) the bot market simulator. Returns new state. */
export function setSimPaused(paused: boolean): Promise<SimState> {
  return request<SimState>(paused ? "/sim/pause" : "/sim/resume", { method: "POST" });
}
