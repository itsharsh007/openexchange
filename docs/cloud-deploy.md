# Cloud Deploy Guide

**No credit card required for any step here.**

Two surfaces to deploy:
- **Gateway** → Render (free tier, no credit card, Docker-native)
- **Frontend** → GitHub Pages (free, CDN, zero extra accounts)

Kafka, Postgres, and the risk service are not deployed to free tier — they run locally and are
demonstrated via `make seed` + a recorded walkthrough.

> **Why Render, not Koyeb?** Koyeb dropped its free tier in 2026 (now a paid Pro plan with a card).
> Render's free web service needs no card: 750 instance-hours/month, HTTPS, GitHub auto-deploy. The
> trade-off is that a free service spins down after 15 min idle and cold-starts in ~1 min on the
> next request — fine for a demo. The blueprint lives at [`deploy/render.yaml`](../deploy/render.yaml).

---

## 1. Gateway → Render (~5 minutes)

### Sign up

Go to [render.com](https://render.com) → **Get Started** → "Sign in with GitHub". No credit card.

### Create the service (dashboard — simplest)

1. **New +** → **Web Service** → connect/select `itsharsh007/openexchange`
2. Branch: `master` | Language: **Docker**
3. **Root Directory:** `gateway` | Dockerfile Path: `Dockerfile`
4. Region: **Frankfurt** | Instance Type: **Free** | Health Check Path: `/healthz`
5. Environment variables:
   ```
   ENGINE_MODE         = mock
   PORT                = 8080
   CORS_ALLOWED_ORIGIN = https://itsharsh007.github.io
   JWT_SECRET          = <paste: openssl rand -hex 32>
   ```
6. Click **Create Web Service**

First deploy ~3–5 min (Docker build). After that, every push to `master` auto-deploys.

### Verify

```bash
curl https://<name>.onrender.com/healthz
# {"status":"ok"}    (first hit after idle may take ~1 min to wake)
```

### Note your URLs

Copy these — you'll need them for the frontend build:

```
VITE_API_BASE = https://<name>.onrender.com
VITE_WS_URL   = wss://<name>.onrender.com/ws
```

---

## 2. Frontend → GitHub Pages (~2 minutes)

### One-time repo settings

1. GitHub repo → **Settings** → **Pages** → Source: **GitHub Actions**
2. **Settings** → **Variables** (not Secrets — these are baked into the static bundle):
   - `VITE_API_BASE` = `https://<name>.onrender.com`
   - `VITE_WS_URL` = `wss://<name>.onrender.com/ws`

### Deploy

Push anything to `master` — the `.github/workflows/pages.yml` workflow builds and deploys
automatically. Or trigger manually: **Actions** → **Pages** → **Run workflow**.

### Live URL

```
https://itsharsh007.github.io/openexchange
```

---

## Redeploying after code changes

```bash
git push origin master
# CI runs → docker.yml builds images → pages.yml deploys frontend
# Render auto-deploys gateway on push (autoDeploy in render.yaml)
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
