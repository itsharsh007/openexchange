# ADR 0007 — Password authentication with access + refresh tokens

- **Status:** Accepted
- **Date:** 2026-06-23

## Context
Until now the only way to get a token was `POST /auth/demo`: an anonymous, short-lived JWT minting a
fresh throwaway account per session (ADR 0006). That is perfect for the one-click public link — no
friction, play money — but it isn't *authentication*. There's no identity that persists across
sessions, no credential to verify, and nothing to demonstrate the auth concerns a real system has:
password storage, token lifetimes, session restoration, and replay resistance.

We want a genuine account system **without** sacrificing the frictionless public demo, and the public
deployment has a hard constraint: the free Render tier runs one small service with **no database**.

## Decision
Add password-backed auth in the gateway, **wired only when a `DATABASE_URL` is configured**:

- **Users** live in a Postgres `users` table (`account_id` PK, unique normalized `email`,
  `password_hash`), created by the engine's Flyway migration `V2__users.sql` — the engine owns the
  schema; the gateway reads/writes this one table through a small `UserStore` interface (Postgres
  impl for the full stack, in-memory impl for tests).
- **Passwords** are hashed with **bcrypt (cost 12)** — never stored or logged in plaintext.
- **Two token kinds**, both HS256 over the shared secret:
  - *access* — `sub = account_id`, ~15 min, sent as the Bearer token on every request.
  - *refresh* — `sub = account_id`, `kind = "refresh"`, ~7 days, accepted **only** at
    `POST /auth/refresh`, which mints a new access token and **rotates** the refresh token.
  The `kind` claim is the replay guard: `JWTAuth` rejects `kind=refresh`, so a refresh token can't be
  used as an access token, and `/auth/refresh` requires it, so an access token can't be used to refresh.
- **Endpoints:** `POST /auth/signup`, `/auth/login`, `/auth/refresh`. Login returns an identical error
  whether the email is unknown or the password is wrong, so registered emails can't be enumerated.
- **Guest stays.** `/auth/demo` is unchanged. On the public link (no `DATABASE_URL`) the signup/login
  routes simply aren't registered, so it remains guest-only and one-click. In the full stack the
  dashboard shows a login screen **with a "continue as guest" button**.

### Client token storage
The access token is held in memory; the refresh token is persisted to `localStorage` and used to
restore the session on reload (and to transparently recover from a 401 mid-session). This trades a
little XSS exposure for a working cross-origin reload. **The hardening step** is to move the refresh
token to an `httpOnly; Secure; SameSite` cookie — deferred because the public split-origin layout
(GitHub Pages frontend → Render gateway) makes credentialed cookies awkward (`SameSite=None` + exact
CORS origin + credentialed requests). Documented here so the tradeoff is explicit, not accidental.

## Consequences
- The project now demonstrates real auth — bcrypt, short access tokens, rotating refresh tokens,
  replay-resistant token kinds, session restore — the auth concerns a production system must get right.
- Auth is **optional and config-gated**: same binary, different environment. The public demo keeps
  its zero-friction guest flow; the full stack gains real accounts. (Twelve-Factor config.)
- Edge hardening shipped alongside: CORS is an **origin allowlist** that reflects the matched origin
  (not a blanket `*`), conservative security headers are always set, and the gateway **refuses to
  boot on the default JWT secret** unless `ALLOW_INSECURE_SECRET=1` (local dev escape hatch).
- Refresh tokens are stateless (not stored server-side), so they can't be individually revoked before
  expiry. Acceptable at this scale; a revocation list / token-version column is the next step if
  needed. Rotation already limits the window of a leaked refresh token.
