import { API_BASE, DEMO_TOKEN } from "../config";

// Session/auth token store.
//
// The dashboard needs a bearer token for every REST call and the WS upgrade.
// There are two ways to get one:
//   1. A build-time VITE_DEMO_TOKEN (handy for local dev / a pinned token).
//   2. The gateway's anonymous demo issuer: POST /auth/demo -> { token, ... }.
//
// We hold the active token in a module-level variable (not React state) because
// the REST client and the imperative WebSocket both read it outside the render
// cycle. ensureSession() is idempotent: it returns the existing token, or fetches
// one once. The in-flight promise is cached so concurrent callers share a fetch.

let token = DEMO_TOKEN;
let accountId = "";
let inflight: Promise<string> | null = null;

/** The current bearer token, or "" if none has been obtained yet. */
export function authToken(): string {
  return token;
}

/** The demo account this session trades as (set once a session is obtained). */
export function demoAccountId(): string {
  return accountId;
}

interface DemoSession {
  token: string;
  accountId: string;
  expiresInSeconds: number;
}

/**
 * Ensure we have a usable bearer token, fetching an anonymous demo session from
 * the gateway if one wasn't provided at build time. Safe to call repeatedly.
 */
export async function ensureSession(): Promise<string> {
  if (token) return token;
  if (inflight) return inflight;
  inflight = (async () => {
    const res = await fetch(`${API_BASE}/auth/demo`, { method: "POST" });
    if (!res.ok) throw new Error(`demo auth failed: HTTP ${res.status}`);
    const data = (await res.json()) as DemoSession;
    token = data.token;
    accountId = data.accountId;
    return token;
  })();
  try {
    return await inflight;
  } finally {
    inflight = null;
  }
}
