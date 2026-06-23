import { API_BASE, DEMO_TOKEN } from "../config";

// Session / auth-token store.
//
// The dashboard needs a bearer token for every REST call and the WS upgrade.
// There are several ways to get one:
//   1. A build-time VITE_DEMO_TOKEN (handy for local dev / a pinned token).
//   2. Anonymous guest: POST /auth/demo -> { token, accountId } (the public link).
//   3. Password auth (full stack): POST /auth/signup | /auth/login, which return
//      an access token + a refresh token.
//
// Token handling (see docs/adr/0007-real-auth.md):
//   - access token  — held in this module-level variable (in memory). The REST
//     client and the imperative WebSocket both read it outside the render cycle.
//   - refresh token — persisted to localStorage so a reload restores the session.
//     This trades a little XSS exposure for UX; an httpOnly cookie is the next step.

const REFRESH_KEY = "oex_refresh";

let token = DEMO_TOKEN; // access token
let accountId = "";
let inflight: Promise<string> | null = null;

/** The current bearer (access) token, or "" if none has been obtained yet. */
export function authToken(): string {
  return token;
}

/** The account this session trades as (set once a session is obtained). */
export function currentAccountId(): string {
  return accountId;
}

/** Back-compat alias used by the dashboard header. */
export function demoAccountId(): string {
  return accountId;
}

/** Whether we currently hold an access token. */
export function isAuthenticated(): boolean {
  return token !== "";
}

interface TokenResponse {
  token?: string; // guest (/auth/demo)
  accessToken?: string; // password auth
  refreshToken?: string;
  accountId: string;
}

function adopt(data: TokenResponse) {
  token = data.accessToken ?? data.token ?? "";
  accountId = data.accountId;
  if (data.refreshToken) localStorage.setItem(REFRESH_KEY, data.refreshToken);
}

// POST JSON to an auth endpoint, throwing the gateway's error message on failure.
async function authPost(path: string, body?: unknown): Promise<TokenResponse> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((data as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return data as TokenResponse;
}

/**
 * Ensure we have a guest token, fetching an anonymous demo session if one wasn't
 * provided at build time. Idempotent; concurrent callers share one fetch.
 */
export async function ensureGuest(): Promise<string> {
  if (token) return token;
  if (inflight) return inflight;
  inflight = (async () => {
    adopt(await authPost("/auth/demo"));
    return token;
  })();
  try {
    return await inflight;
  } finally {
    inflight = null;
  }
}

/** Register a new account and log in. Throws with the gateway's reason on failure. */
export async function signup(email: string, password: string): Promise<void> {
  adopt(await authPost("/auth/signup", { email, password }));
}

/** Log in with email + password. Throws with the gateway's reason on failure. */
export async function login(email: string, password: string): Promise<void> {
  adopt(await authPost("/auth/login", { email, password }));
}

/**
 * Exchange a stored refresh token for a fresh access token. Returns false (and
 * clears the stale token) if there's none or it's rejected. Used both to restore
 * a session on load and to recover from a 401 mid-session.
 */
export async function refreshAccess(): Promise<boolean> {
  const refresh = localStorage.getItem(REFRESH_KEY);
  if (!refresh) return false;
  try {
    adopt(await authPost("/auth/refresh", { refreshToken: refresh }));
    return true;
  } catch {
    localStorage.removeItem(REFRESH_KEY);
    token = "";
    return false;
  }
}

/** Clear the session (access token + persisted refresh token). */
export function logout(): void {
  token = "";
  accountId = "";
  localStorage.removeItem(REFRESH_KEY);
}

// Back-compat: older callers used ensureSession() for the guest flow.
export const ensureSession = ensureGuest;
