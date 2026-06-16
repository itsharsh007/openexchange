# Roadmap — including "Going Public / Open Source"

This project is built so it can later be open-sourced and let real users try it. Because it is a
**play-money simulation** (no real funds, no regulated assets), it sidesteps the legal/financial
risk a real exchange faces. The items below are incremental additions the architecture already
leaves room for — not rewrites.

## Phase build-out (current)
- [ ] Phase 0 — Init, scaffold, contracts, infra
- [ ] Phase 1 — Matching engine core
- [ ] Phase 2 — Go gateway
- [ ] Phase 3 — Python risk/ML service
- [ ] Phase 4 — React dashboard
- [ ] Phase 5 — Infra, observability, deploy

## Going public (later)
- [ ] **User signup + isolated accounts** — each visitor gets an independent play-money wallet.
- [ ] **Per-user sandbox / reset** — fresh demo balance; can't affect others.
- [ ] **Abuse protection** — per-account rate limits (in place), signup CAPTCHA, bot detection
      (the anomaly model already helps here).
- [ ] **Resource caps & autoscaling** — cap per-user open orders; autoscale the gateway under load.
- [ ] **Production hardening** — HTTPS/TLS, managed secrets, no debug endpoints, security headers.
- [ ] **"Demo mode" disclaimer** — explicit play-money / not-investment-advice banner.
- [ ] **Public docs site** — host `docs/` as a site; add a guided tour.

## Cost expectations
- Local dev: **$0**.
- Low-traffic public demo: **$0** on Oracle Cloud Always-Free or small Fly.io footprint.
- Viral / hundreds concurrent: ~**$10–40/month** on a modest VPS; scale only the hot path
  (gateway), not the whole stack.

## Compliance target
The deployment follows the **Twelve-Factor App** methodology (extended to 15). See
[`twelve-factor.md`](twelve-factor.md) for the per-factor map.
