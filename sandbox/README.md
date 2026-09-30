# sandbox — Kali Execution Container

> Quick local overview. For the full, kept-current reference (the one exec path, timeouts/caps, what's agent-reachable vs not), see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the unified Dockerized Kali Linux environment where pentest commands run. The agent's `docker exec` tool runs, the operator's Bash (`tmux`) tab, and the graphical Desktop (`noVNC`/XFCE) tab all share this single container. Its default route crosses gateway policy: `block` denies external egress, `ask` opens approved destinations, and `allow` permits external destinations except unconditional/configured blocks.

## Security constraints

| Constraint | Value | Reason |
|---|---|---|
| User | `1000:1000` (`pentester`) | Non-root execution for agent commands and desktop session |
| Root filesystem | Writable | Unified environment: XFCE, D-Bus, VNC, and operator tools require runtime state (`~/.cache`, `~/.config`, `/run/user/1000`) |
| Container capabilities | `NET_RAW`, `NET_ADMIN`, minimal prep caps (`CHOWN`, `FOWNER`, `DAC_OVERRIDE`, `SETUID`, `SETGID`) | Root PID 1 needs `NET_ADMIN` once to replace default route, prep caps for privilege drop; raw-packet tools need `NET_RAW`. Agent commands still run as UID 1000. |
| DNS | `1.1.1.1` / `1.0.0.1` (Cloudflare) | Same resolver the orchestrator uses, so both agree on a name's IP before the gateway opens a rule. DNS still egresses through the gateway (udp/53 accepted); resolution is not the trust boundary — the connection to the resolved IP is gated by nftables. |
| `no-new-privileges` | `false` | Required for nmap's file capabilities to be effective for UID 1000. This is an explicit hardening trade-off, not an escalation guarantee. |

## Volumes

| Host path | Container path | Access |
|---|---|---|
| `~/.yummi/wordlists` | `/wordlists` | Read-only |
| `~/.yummi/workspace` | `/workspace` | Read-write and persistent on the host |
| `~/.yummi/payloads` | `/payloads` | Read-only |

## Sandbox MCP Server

The Kali image includes a compiled binary at `/usr/local/bin/yummi-sandbox-mcp` (built from the host's `sandbox-mcp/` Go module via multi-stage build). It registers 30 pentest and recon tools that can be invoked via MCP stdio over persistent `docker exec -i` by orchestrator's Eino agent backend.

## Adding a tool

Edit `sandbox/Dockerfile`. Tools must be installed from the Kali rolling repository:

```dockerfile
RUN apt-get install -y --no-install-recommends \
    mytool \
    && apt-get clean && rm -rf /var/lib/apt/lists/*
```

There is **no executable allowlist** to register the tool in. Keep the informational `tools` list in the relevant `persona/v0.1/skills/*.yaml` file aligned with the image, and add a short methodology note there when needed. The validator gates on *risk*, not tool identity. The build verifies the core documented commands (`nmap`, `curl`, `ffuf`, `gobuster`, `nikto`, `whatweb`, `grep`, `rg`, `git`).

## Entrypoint

`entrypoint.sh` runs as the container's PID 1. It:
1. Replaces the default route with the gateway address (`172.20.0.2`, fail-closed). Compose's `depends_on` health condition ensures gateway is ready before this script starts.
2. Initializes `/run/user/1000` and drops privileges to the `pentester` user (UID 1000).
3. Starts Xtigervnc on `:1` and launches an XFCE desktop session with Yummi branding.
4. Executes `websockify` on port 6080 (`/ws/desktop`) bridging WebSocket to TigerVNC as the container's foreground process.

Agent commands and the Bash terminal reach this same container via separate `docker exec` invocations (`sandbox.Bridge` and `terminal.go`), independent of the desktop process tree.

## Healthcheck

```yaml
test: ["CMD-SHELL", "nmap --version > /dev/null 2>&1"]
```

sandbox is healthy when nmap is reachable, confirming the core toolset is present.

## Network position

```
sandbox (172.20.0.10) ──── yummi_internal ──── gateway (172.20.0.2)
                                                │
                                         yummi_external
                                                │
                                         External Network
```

The sandbox has one attached network and its default route points at gateway, so ordinary UID 1000 tool traffic crosses the gateway's packet policy. The container nevertheless receives `NET_ADMIN` during startup; a container-root compromise would therefore weaken this boundary. Treat the gateway path as strong defense in depth, not as a guarantee against a sandbox-container escape or root takeover.
