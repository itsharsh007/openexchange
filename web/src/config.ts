// Centralized runtime configuration, sourced from Vite env vars.
//
// WHY a single module: every other file imports from here, so there is exactly
// one place that knows the gateway's address. Defaults point at the local Go
// gateway so `npm run dev` works out of the box with `make up`.
//
// Override per-environment with a `.env.local` (git-ignored) or real env vars:
//   VITE_API_BASE=https://api.openexchange.example
//   VITE_WS_URL=wss://api.openexchange.example/ws

export const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
export const WS_URL = import.meta.env.VITE_WS_URL ?? "ws://localhost:8080/ws";
// Whether the gateway has password auth (signup/login) wired — true only for the
// full-stack build (Postgres). The public link leaves this false and offers the
// frictionless guest session instead. See web/Dockerfile + deploy/docker-compose.
export const AUTH_ENABLED = import.meta.env.VITE_AUTH_ENABLED === "true";
// Local dev token — set VITE_DEMO_TOKEN in web/.env.local (git-ignored).
// In production this comes from a real auth flow.
export const DEMO_TOKEN: string = import.meta.env.VITE_DEMO_TOKEN ?? "";

// Tick → price convention.
// ---------------------------------------------------------------------------
// The engine and proto carry prices as integer *ticks* to avoid floating-point
// money bugs (see proto/openexchange.proto and the engine docs). One tick
// = one cent here, so the human price = price_ticks / 100. Quantities are whole
// units (shares/contracts) and are NOT scaled.
export const TICKS_PER_UNIT = 100;
