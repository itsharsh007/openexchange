# Cloud Deploy Guide

**No credit card required for any step here.**

Two surfaces to deploy:
- **Gateway** → Koyeb (free, no sleep, Docker-native)
- **Frontend** → GitHub Pages (free, CDN, zero extra accounts)

Kafka, Postgres, and the risk service are not deployed to free tier — they run locally and are
demonstrated via `make seed` + a recorded walkthrough.

---

## 1. Gateway → Koyeb (~5 minutes)

### Sign up

Go to [koyeb.com](https://koyeb.com) → "Sign up with GitHub". No credit card.

### Create the service (dashboard — simplest)

1. **New Service** → **GitHub**
2. Repo: `itsharsh007/openexchange` | Branch: `main`
3. Builder: **Dockerfile** | Context: `gateway` | Dockerfile path: `gateway/Dockerfile`
4. Port: `8080` | Protocol: **HTTP**
5. Environment variables:
   ```
   ENGINE_MODE = mock
   PORT        = 8080
   JWT_SECRET  = <paste: openssl rand -hex 32>
   ```
6. Region: **Frankfurt (fra)** — best latency from India on free tier
7. Instance type: **Nano** (free)
8. Min instances: **1** (keeps it warm — no cold starts)
9. Click **Deploy**

First deploy ~3 min (Docker build). After that: ~45s rolling deploy.

### Verify

```bash
curl https://<your-app>.koyeb.app/healthz
# {"status":"ok"}
```

### Note your URLs

Copy these — you'll need them for the frontend build:

```
VITE_API_BASE = https://<your-app>.koyeb.app
VITE_WS_URL   = wss://<your-app>.koyeb.app/ws
```

---

## 2. Frontend → GitHub Pages (~2 minutes)

### One-time repo settings

1. GitHub repo → **Settings** → **Pages** → Source: **GitHub Actions**
2. **Settings** → **Variables** (not Secrets — these are baked into the static bundle):
   - `VITE_API_BASE` = `https://<your-app>.koyeb.app`
   - `VITE_WS_URL` = `wss://<your-app>.koyeb.app/ws`

### Deploy

Push anything to `main` — the `.github/workflows/pages.yml` workflow builds and deploys
automatically. Or trigger manually: **Actions** → **Pages** → **Run workflow**.

### Live URL

```
https://itsharsh007.github.io/openexchange
```

---

## Redeploying after code changes

```bash
git push origin main
# CI runs → docker.yml builds images → pages.yml deploys frontend
# Koyeb auto-deploys gateway on push (configured in dashboard)
```

Everything rolls forward in parallel. Total time: ~3 minutes.

---

## What's NOT deployed (and how to show it)

| Service | Why not deployed | How to show it |
|---|---|---|
| Java engine | Needs 512 MB+ VM, no free tier fits | Local demo video |
| Kafka | No free managed option without card | `make seed` walkthrough in README |
| Postgres | Free tiers exist but risk service needs Kafka too | Grafana screenshot in docs |
| Risk service | Depends on Kafka | Risk signal demo in build-journal |

Add this to your README (already there) so readers know it's intentional, not incomplete.
