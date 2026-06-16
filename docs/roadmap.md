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

## Known limitations (to harden)
- **`trade_id` is an in-memory per-symbol sequence** (`AAPL-T1`, `AAPL-T2`, …) that resets when the
  engine restarts, so it is not durably unique across restarts. The ledger insert is idempotent on
  `trade_id`, which means after a restart the first new trade could collide with an old row and be
  skipped. Proper fix: derive engine state by **replaying the event log** (Kafka + ledger) on
  startup so sequences continue, or switch `trade_id` to a UUID. Tracked for the Kafka phase.
- **Trade publish is best-effort** (after the ledger commit), so the `trades` topic can miss an event
  during a broker outage even though the ledger has it. Durable fix: transactional outbox — see
  [`adr/0003-trade-event-streaming.md`](adr/0003-trade-event-streaming.md). The outbox row also
  carries a stable id, which subsumes the `trade_id` limitation above.

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
