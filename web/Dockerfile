# ── Stage 1: build the static bundle ────────────────────────────────────────
FROM node:20-alpine AS build
WORKDIR /app

# Install deps first (separate layer) so they're cached unless package*.json change.
COPY package.json package-lock.json* ./
RUN npm ci || npm install

COPY . .

# Build-time env vars are baked into the bundle (Vite inlines VITE_* at build).
# Override at build time: --build-arg VITE_API_BASE=... etc.
ARG VITE_API_BASE
ARG VITE_WS_URL
ENV VITE_API_BASE=$VITE_API_BASE
ENV VITE_WS_URL=$VITE_WS_URL
RUN npm run build

# ── Stage 2: serve with nginx ────────────────────────────────────────────────
FROM nginx:1.27-alpine AS serve
# SPA routing: fall back to index.html so client-side routes resolve.
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
