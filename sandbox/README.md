# sandbox — Kali Execution Container

> For the full, kept-current reference, see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the unified Dockerized Kali Linux environment where pentest commands run. The agent's `docker exec` tool runs, the operator's Bash (`tmux`) tab, and the graphical Desktop (`noVNC`/XFCE) tab all share this single container. Its default route crosses gateway policy: `block_all` denies external egress unconditionally, `block_all_except_rules` permits traffic matching active rules or configured scope, `allow_all` permits external destinations except unconditional/configured blocks, and `ask_every_time` prompts the operator for interactive authorization on each new destination.

---

## Security Constraints

| Constraint | Value | Reason |
|---|---|---|
| User | `1000:1000` (`pentester`) | Non-root execution for agent commands and desktop session. |
| Root filesystem | Writable | Unified environment: XFCE, D-Bus, VNC, and operator tools require runtime state (`~/.cache`, `~/.config`, `/run/user/1000`). |
| Container capabilities | `NET_RAW`, `NET_ADMIN`, minimal prep caps (`CHOWN`, `FOWNER`, `DAC_OVERRIDE`, `SETUID`, `SETGID`) | The root entrypoint needs `NET_ADMIN` to replace the default route, and prep caps for filesystem setup/privilege drop; raw-packet tools need `NET_RAW`. Agent commands still run as UID 1000. |
| DNS | `1.1.1.1` / `1.0.0.1` (Cloudflare) | Cloudflare upstreams match the orchestrator resolver. Answers may still differ over time. Gateway-forwarded UDP/53 is permitted only in non-`block_all` modes, after configured blocks; resolving a name grants no target access. |
| `no-new-privileges` | `false` | Required for nmap's file capabilities to be effective for UID 1000. |

---

## Volumes

| Host path | Container path | Access |
|---|---|---|
| `$YUMMI_HOST_DATA_DIR/wordlists` | `/wordlists` | Read-only |
| `$YUMMI_HOST_DATA_DIR/workspace` | `/workspace` | Read-write and persistent on the host |
| `$YUMMI_HOST_DATA_DIR/payloads` | `/payloads` | Read-only |

---

`YUMMI_HOST_DATA_DIR` defaults to `~/.yummi`. The writable workspace is a host bind mount: sandbox commands can change its contents.

## Sandbox MCP Server

The Kali image includes a compiled binary at `/usr/local/bin/yummi-sandbox-mcp` (built from the host's `sandbox-mcp/` Go module via multi-stage build). It registers typed and cataloged pentest wrappers, recon utilities, persistent tmux terminals, managed Chromium tools, and sandbox-only web research (`web_search`, `web_fetch`). Discover schemas with MCP `tools/list`; availability is determined by the current source/catalog and image, rather than a fixed documented count. Chromium's connection guard uses an ephemeral loopback proxy; arbitrary shell/desktop applications do not automatically use it.

Tools are invoked via MCP stdio over persistent `docker exec -i` by the orchestrator.

---

## Entrypoint

Compose sets `init: true`, so Docker's init is PID 1 and reaps orphan processes. The root `entrypoint.sh` child:

1. Replaces the default route with the gateway address (`172.20.0.2`, fail-closed).
2. Prepares workspace/runtime ownership and attempts `/dev/net/tun` creation. The default capability set omits `MKNOD`; failure is nonfatal, leaving VPN unavailable unless the device/capability is deliberately supplied.
3. Starts Xtigervnc on `:1` and an XFCE desktop session as `pentester` (UID 1000), with Yummi branding.
4. Executes `websockify` on container port 6080, bridging to TigerVNC. Orchestrator exposes the operator `/ws/desktop` bridge; no raw VNC/6080 port is published on the host. Websockify remains a child of Docker's init and runs under the entrypoint's root identity.

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

The sandbox has one attached network and its default route points at gateway, so ordinary UID 1000 tool traffic crosses the gateway's packet policy. The container receives `NET_ADMIN` and selected tools have file capabilities, with `no-new-privileges:false`; those powers or a container-root compromise can weaken routing/source assumptions. Treat the gateway path as strong defense in depth, not as a guarantee against a sandbox-container escape or root takeover.
