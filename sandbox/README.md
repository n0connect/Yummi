# sandbox — Kali Execution Container

> For the full, kept-current reference, see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the unified Dockerized Kali Linux environment where pentest commands run. The agent's `docker exec` tool runs, the operator's Bash (`tmux`) tab, and the graphical Desktop (`noVNC`/XFCE) tab all share this single container. Its default route crosses gateway policy: `block_all` denies external egress unconditionally, `block_all_except_rules` permits traffic matching active rules or configured scope, `allow_all` permits external destinations except unconditional/configured blocks, and `ask_every_time` prompts the operator for interactive authorization on each new destination.

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

The Kali image includes a compiled binary at `/usr/local/bin/yummi-sandbox-mcp` (built from the host's `sandbox-mcp/` Go module via multi-stage build). It registers 58 native pentest, recon, persistent terminal, browser automation, and web research tools across 6 tiers:
- **Tier 1 (6 Typed Pentest Tools)**: `nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`.
- **Tier 2 (32 Cataloged Pentest Tools)**: CLI-wrapped pentesting and analysis tools (`hydra`, `sqlmap`, `wpscan`, `commix`, `amass`, `sublist3r`, `enum4linux`, etc.).
- **Tier 3 (2 Go Recon Utilities)**: Pure-Go network utilities (`consolidate_hosts`, `passive_recon_batch`).
- **Tier 4 (7 Persistent Tmux Tools)**: Stateful terminal multiplexing (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`).
- **Tier 5 (9 Browser Automation Tools)**: Headless Chromium automation (`browser_open`, `browser_snapshot`, `browser_interact`, `browser_eval`, `browser_screenshot`, `browser_storage`, `browser_tabs`, `browser_logs`, `browser_session`) routed via in-sandbox connection proxy (`browser_proxy.go`).
- **Tier 6 (2 Sandboxed Web Research Tools)**: Controlled external research and retrieval (`web_search`, `web_fetch`) with untrusted content containment.

Tools are invoked via MCP stdio over persistent `docker exec -i` by the orchestrator.

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
