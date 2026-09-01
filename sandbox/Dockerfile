# ── Build stage: the sandbox-resident MCP server ────────────────────────────
# sandbox-mcp/go.mod pins go 1.26.5 (the MCP SDK's own floor is 1.25.0; go mod
# tidy resolved the transitive minimum up to 1.26.5) — independent of
# orchestrator/go.mod's go 1.22, since this is a deliberately separate module.
FROM golang:1.26.6-bookworm AS mcpbuilder
WORKDIR /build
COPY sandbox-mcp/go.mod sandbox-mcp/go.sum ./
RUN go mod download
COPY sandbox-mcp/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/yummi-sandbox-mcp .

# ── Runtime stage: Kali ──────────────────────────────────────────────────────
FROM kalilinux/kali-rolling:latest

LABEL org.opencontainers.image.title="Yummi sandbox"
LABEL org.opencontainers.image.description="Isolated Kali pentest container — network traffic gated by gateway"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.source="https://github.com/n0connect/yummi"

ENV DEBIAN_FRONTEND=noninteractive

# Pentest tools only — gateway runs in its own container (yummi-gateway).
# nftables/iptables/iproute2 retained for tools that use raw sockets.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      amass \
      ca-certificates \
      crunch \
      curl \
      dirb \
      dnsenum \
      enum4linux \
      ffuf \
      git \
      gobuster \
      hydra \
      iptables \
      iproute2 \
      jq \
      libcap2-bin \
      medusa \
      netcat-openbsd \
      nftables \
      nikto \
      nmap \
      nuclei \
      onesixtyone \
      smbclient \
      snmp \
      socat \
      sqlmap \
      subfinder \
      tcpdump \
      theharvester \
      tmux \
      wafw00f \
      wget \
      whatweb \
      wpscan \
      ripgrep && \
    if [ -f /usr/lib/nmap/nmap ]; then setcap cap_net_admin,cap_net_raw=eip /usr/lib/nmap/nmap; fi && \
    for tool in nmap curl ffuf gobuster nikto whatweb grep rg git tmux; do command -v "$tool" >/dev/null; done && \
    rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 pentester && \
    useradd --uid 1000 --gid pentester --create-home --shell /bin/bash pentester

COPY sandbox/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

COPY --from=mcpbuilder /out/yummi-sandbox-mcp /usr/local/bin/yummi-sandbox-mcp
RUN chmod 0755 /usr/local/bin/yummi-sandbox-mcp

# Pentest commands from orchestrator run as UID 1000 via docker exec --user.
# The entrypoint (root) sets up routing through gateway, then drops to sleep.
WORKDIR /home/pentester

HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=60s \
    CMD nmap --version > /dev/null 2>&1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
