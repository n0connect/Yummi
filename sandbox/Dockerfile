# ── Sandbox — unified pentest + desktop environment ──────────────────────────
# The ONE Kali environment both the agent's exec path (sandbox.Bridge /
# sandboxmcp.Client, docker exec) and the browser-reachable graphical desktop
# (/ws/desktop → noVNC → this container's own websockify+TigerVNC) run
# against. There used to be two separate images/containers here (a headless
# one for the agent, a bigger opt-in one for the GUI) — unified into a single
# environment so there is zero tool/environment drift between what the agent
# can reach and what the operator sees when they open the Desktop tab: same
# filesystem, same installed tools, same running process tree. The operator
# can always drop into it via the Bash tab (tmux) or the Desktop tab (XFCE) —
# neither is a different box. See
# docs/superpowers/specs/2026-09-01-browser-linux-pwnbox-design.md.
#
# Trade-off accepted knowingly: this container is NOT read-only (XFCE/VNC/
# dbus need scattered writable session state) and always runs the desktop
# stack, not just on request. The network trust boundary is unaffected either
# way — this container's default route still goes through gateway, same as
# before.

FROM golang:1.26.6-bookworm AS mcpbuilder
WORKDIR /build
COPY sandbox-mcp/go.mod sandbox-mcp/go.sum ./
RUN go mod download
COPY sandbox-mcp/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/yummi-sandbox-mcp .

# ── recontools stage: modern bug-bounty recon binaries not in Kali apt ───────
# ProjectDiscovery's httpx/dnsx/alterx and lc/gau give the senior recon
# methodology (persona/v0.1/skills/osint-enum.yaml) its live-probing,
# resolution, permutation, and historical-URL steps — the pieces subfinder/
# amass/theHarvester alone don't cover. Installed here and copied as static
# binaries so the runtime image needs no Go toolchain. @latest is intentional
# and consistent with this image's design: the base is kali-rolling:latest and
# subfinder/nuclei come from rolling apt, so the sandbox is deliberately a
# rolling/latest environment, not a pinned one.
FROM golang:1.26.6-bookworm AS recontools
ENV CGO_ENABLED=0 GOBIN=/out
RUN go install github.com/projectdiscovery/httpx/cmd/httpx@latest && \
    go install github.com/projectdiscovery/dnsx/cmd/dnsx@latest && \
    go install github.com/projectdiscovery/alterx/cmd/alterx@latest && \
    go install github.com/projectdiscovery/katana/cmd/katana@latest && \
    go install github.com/projectdiscovery/naabu/v2/cmd/naabu@latest && \
    go install github.com/projectdiscovery/tlsx/cmd/tlsx@latest && \
    go install github.com/LukaSikic/subzy@latest && \
    go install github.com/BishopFox/jsluice/cmd/jsluice@latest && \
    go install github.com/hahwul/dalfox/v2@latest && \
    go install github.com/lc/gau/v2/cmd/gau@latest

FROM kalilinux/kali-rolling:latest

LABEL org.opencontainers.image.title="Yummi sandbox"
LABEL org.opencontainers.image.description="Unified Kali pentest + desktop container — agent exec and browser-reached XFCE desktop share this one environment, network traffic gated by gateway"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.source="https://github.com/n0connect/yummi"

ENV DEBIAN_FRONTEND=noninteractive

# Pentest tool set plus the desktop stack: xfce4 (window manager + panel +
# session), a terminal and file manager, tigervnc (the VNC server), novnc +
# websockify (browser-reachable VNC over a WebSocket), dbus-x11 (XFCE needs a
# session bus), and firefox-esr for in-browser recon/testing work from
# inside the sandbox itself.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      amass \
      arjun \
      binutils \
      binwalk \
      ca-certificates \
      checksec \
      crunch \
      curl \
      dirb \
      dnsenum \
      dnsutils \
      enum4linux \
      ffuf \
      file \
      gdb \
      git \
      gobuster \
      hydra \
      iptables \
      iproute2 \
      jq \
      jwt-tool \
      libcap2-bin \
      ltrace \
      medusa \
      netcat-openbsd \
      nftables \
      nikto \
      nmap \
      nuclei \
      onesixtyone \
      patchelf \
      radare2 \
      smbclient \
      snmp \
      socat \
      sqlmap \
      strace \
      subfinder \
      tcpdump \
      theharvester \
      tmux \
      wafw00f \
      wget \
      whatweb \
      wpscan \
      ripgrep \
      xfce4 \
      xfce4-terminal \
      thunar \
      dbus-x11 \
      tigervnc-standalone-server \
      tigervnc-common \
      novnc \
      websockify \
      firefox-esr \
      wireshark \
      x11-xserver-utils \
      burpsuite \
      imagemagick && \
      if [ -f /usr/lib/nmap/nmap ]; then setcap cap_net_admin,cap_net_raw=eip /usr/lib/nmap/nmap; fi && \
      for tool in nmap curl ffuf gobuster nikto whatweb grep rg git tmux Xtigervnc websockify xfce4-session convert r2 radare2 gdb checksec binwalk strace ltrace file strings objdump readelf dig; do command -v "$tool" >/dev/null; done && \
      rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 pentester && \
    useradd --uid 1000 --gid pentester --create-home --shell /bin/bash pentester && \
    # wireshark's dumpcap needs this group to capture without full root —
    # matches nmap's setcap-based (not root-based) capability grant above.
    # Best-effort: the "wireshark" group only exists if the wireshark-common
    # package's debconf prompt (non-interactive here) created it, which isn't
    # guaranteed across Kali rolling snapshots — dumpcap still works as root
    # via `sudo`-less docker exec's own privilege if this doesn't apply, so
    # this must not fail the whole image build over it.
    (usermod -aG wireshark pentester || true)

# nuclei ships with ZERO templates from Kali's apt package — without this,
# every nuclei invocation (vuln-scanning skill, api-pentest's -tags graphql)
# silently matches nothing, no error, no findings, ever. Fetched here as the
# SAME user (pentester, UID 1000) that will actually run nuclei via
# docker exec, so the template path nuclei resolves at build time is the
# exact one it resolves at runtime. This also gives subdomain-takeover
# detection "for free" — nuclei's own templates include a takeover-tagged
# category (`nuclei -tags takeover`), no separate tool needed. Templates are
# baked into the image (not fetched at container start) for a fast, fully
# offline-capable first run; they go stale between image rebuilds, which is
# the same rolling-image trade-off this Dockerfile already makes elsewhere.
RUN su - pentester -c "nuclei -update-templates -silent" 2>&1 | tail -5

# Branded wallpaper — generated at build time (no external asset fetch),
# matching the web UI's own dark theme tokens (--bg-base #0a0c0e, --cyan
# #38bdf8, --text-tertiary-ish #6b7a80 for the tagline) so the desktop
# reads as the SAME product as the browser chrome around it, not a stock
# Kali install with a browser window open to it.
RUN mkdir -p /usr/share/backgrounds/yummi && \
    convert -size 1920x1080 xc:'#0a0c0e' \
      -gravity center \
      -font DejaVu-Sans-Mono-Bold -pointsize 120 -fill '#38bdf8' \
      -annotate +0-40 'YUMMI' \
      -font DejaVu-Sans-Mono -pointsize 28 -fill '#6b7a80' \
      -annotate +0+70 'PENTEST WORKBENCH' \
      /usr/share/backgrounds/yummi/wallpaper.png

COPY sandbox/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

COPY --from=mcpbuilder /out/yummi-sandbox-mcp /usr/local/bin/yummi-sandbox-mcp
RUN chmod 0755 /usr/local/bin/yummi-sandbox-mcp

# Modern recon/web binaries (see the recontools stage) —
# httpx/dnsx/alterx/katana/dalfox/gau/naabu/tlsx/subzy/jsluice. Verify they landed and are runnable, so
# a broken install fails the build rather than surfacing as a mysterious
# "command not found" mid-engagement.
COPY --from=recontools /out/ /usr/local/bin/
RUN for tool in httpx dnsx alterx gau katana dalfox naabu tlsx subzy jsluice; do command -v "$tool" >/dev/null || { echo "recon tool missing: $tool" >&2; exit 1; }; done

# Pentest commands from orchestrator run as UID 1000 via docker exec --user;
# the operator's Bash tab attaches a tmux session as the same user. The
# entrypoint (root) sets up routing + filesystem prep, then drops to
# pentester for the desktop session.
WORKDIR /home/pentester

# 6080: websockify's WebSocket<->VNC bridge, proxied by orchestrator's
# /ws/desktop handler — never published to the host directly, same pattern
# the terminal bridge uses for docker exec.
EXPOSE 6080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=90s \
    CMD curl -sf http://127.0.0.1:6080/ > /dev/null 2>&1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
