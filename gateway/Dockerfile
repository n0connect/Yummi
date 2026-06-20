# ── Build stage ──────────────────────────────────────────────────────────────
FROM rust:1.86-slim-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config libsqlite3-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Cache dependency layer: build a dummy binary first so cargo fetches crates
COPY gateway/Cargo.toml gateway/Cargo.lock ./
RUN mkdir src && echo "fn main() {}" > src/main.rs && echo "" > src/lib.rs && \
    cargo build --release 2>/dev/null || true && \
    rm -rf src \
           target/release/yummi-gateway \
           target/release/deps/yummi_gateway* \
           target/release/.fingerprint/yummi-gateway*

# Build the real binary
COPY gateway/src ./src
RUN cargo build --release

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# nftables: for FORWARD chain and NAT rules
# iproute2: ip(8) utilities
# libsqlite3-0: audit database
# ca-certificates: TLS roots for future mTLS
RUN apt-get update && apt-get install -y --no-install-recommends \
    nftables \
    iproute2 \
    libsqlite3-0 \
    ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Directories for audit DB, IPC socket, and config
RUN mkdir -p /var/lib/yummi/gateway /run/yummi /etc/yummi

COPY --from=builder /build/target/release/yummi-gateway /usr/local/bin/yummi-gateway
COPY gateway/config/default.yaml /etc/yummi/gateway.yaml

# gateway runs as root — required for CAP_NET_ADMIN (nftables management).
# Security is enforced via cap_drop + cap_add in docker-compose.yml.
WORKDIR /root

HEALTHCHECK --interval=10s --timeout=5s --retries=6 --start-period=15s \
    CMD test -S /run/yummi/gateway.sock

ENTRYPOINT ["/usr/local/bin/yummi-gateway", "--config", "/etc/yummi/gateway.yaml"]
