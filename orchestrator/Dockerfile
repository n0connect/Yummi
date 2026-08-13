# --- Build stage ---
# orchestrator/go.mod pins go 1.25.0 (bumped by adding the MCP SDK dependency
# for internal/sandboxmcp — its own floor is 1.25.0).
FROM golang:1.25-bookworm AS builder

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
