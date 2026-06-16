# ── Stage 1: build ───────────────────────────────────────────────────────────
# WHY multi-stage: the final image carries only a static binary (~10MB) — no Go
# toolchain, no source — which shrinks attack surface and image size.
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache deps separately from source so `go mod download` is only re-run when
# go.mod/go.sum change, not on every code edit.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# CGO disabled => a fully static binary that runs on scratch/distroless.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/gateway ./cmd/gateway

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
# distroless: no shell, no package manager — minimal runtime surface.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/gateway /gateway

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
