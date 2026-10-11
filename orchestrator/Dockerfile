# --- Build stage ---
# Must track orchestrator/go.mod's own `go` directive exactly — GOTOOLCHAIN
# defaults to local in this image, so `go mod download` hard-fails instead of
# auto-fetching a newer toolchain if this tag falls behind go.mod's floor
# (confirmed live: a go.mod bump to 1.27.2 broke this build with exactly that
# error before this comment existed).
FROM golang:1.27.2-bookworm AS builder

WORKDIR /build

# Cache dependencies separately
COPY orchestrator/go.mod orchestrator/go.sum ./
RUN go mod download

# Build binary (CGO required for go-sqlite3)
COPY orchestrator/ ./
RUN CGO_ENABLED=1 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w" \
    -o /bin/yummi-orchestrator \
    ./cmd/orchestrator

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    docker.io \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bin/yummi-orchestrator /usr/local/bin/yummi-orchestrator
COPY persona /persona

RUN mkdir -p /var/lib/yummi/orchestrator

EXPOSE 9080

# orchestrator runs as root to access Docker socket; capabilities are dropped in compose.
ENTRYPOINT ["/usr/local/bin/yummi-orchestrator"]
