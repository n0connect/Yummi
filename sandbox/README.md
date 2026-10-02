# sandbox — Kali Execution Container

> For the full, kept-current reference, see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the unified Dockerized Kali Linux environment where pentest commands run. The agent's `docker exec` tool runs, the operator's Bash (`tmux`) tab, and the graphical Desktop (`noVNC`/XFCE) tab all share this single container. Its default route crosses gateway policy: `block` denies external egress, `ask` opens approved destinations, and `allow` permits external destinations except unconditional/configured blocks.

---

## Security Constraints

| Constraint | Value | Reason |
|---|---|---|
| User | `1000:1000` (`pentester`) | Non-root execution for agent commands and desktop session. |
| Root filesystem | Writable | Unified environment: XFCE, D-Bus, VNC, and operator tools require runtime state (`~/.cache`, `~/.config`, `/run/user/1000`). |
| Container capabilities | `NET_RAW`, `NET_ADMIN`, minimal prep caps (`CHOWN`, `FOWNER`, `DAC_OVERRIDE`, `SETUID`, `SETGID`) | Root PID 1 needs `NET_ADMIN` once to replace default route, prep caps for privilege drop; raw-packet tools need `NET_RAW`. Agent commands still run as UID 1000. |
| DNS | `1.1.1.1` / `1.0.0.1` (Cloudflare) | Same resolver the orchestrator uses, so both agree on a name's IP before the gateway opens a rule. DNS still egresses through the gateway (udp/53 accepted); resolution is not the trust boundary — the connection to the resolved IP is gated by nftables. |
| `no-new-privileges` | `false` | Required for nmap's file capabilities to be effective for UID 1000. |

---

## Volumes

| Host path | Container path | Access |
|---|---|---|
| `~/.yummi/wordlists` | `/wordlists` | Read-only |
| `~/.yummi/workspace` | `/workspace` | Read-write and persistent on the host |
| `~/.yummi/payloads` | `/payloads` | Read-only |

---

## Sandbox MCP Server

The Kali image includes a compiled binary at `/usr/local/bin/yummi-sandbox-mcp` (built from the host's `sandbox-mcp/` Go module via multi-stage build). It registers 47 pentest, recon, and persistent terminal session tools (6 typed, 32 cataloged, 2 pure-Go utilities, and 7 tmux-backed terminal verbs) that can be invoked via MCP stdio over persistent `docker exec -i` by orchestrator's Eino agent backend.

---

## Entrypoint

`entrypoint.sh` runs as the container's PID 1:
1. Replaces the default route with the gateway address (`172.20.0.2`, fail-closed).
2. Initializes `/run/user/1000` and drops privileges to the `pentester` user (UID 1000).
3. Starts Xtigervnc on `:1` and launches an XFCE desktop session with Yummi branding.
4. Executes `websockify` on port 6080 (`/ws/desktop`) bridging WebSocket to TigerVNC as the container's foreground process.

Agent commands and the Bash terminal reach this same container via separate `docker exec` invocations (`sandbox.Bridge` and `terminal.go`), independent of the desktop process tree.

---

## Network Position

```text
sandbox (172.20.0.10) ---- yummi_internal ---- gateway (172.20.0.2)
                                                |
                                         yummi_external
                                                |
                                         External Network
```

The sandbox has one attached network and its default route points at gateway, so ordinary UID 1000 tool traffic crosses the gateway's packet policy. The container nevertheless receives `NET_ADMIN` during startup; a container-root compromise would therefore weaken this boundary. Treat the gateway path as strong defense in depth, not as a guarantee against a sandbox-container escape or root takeover.
