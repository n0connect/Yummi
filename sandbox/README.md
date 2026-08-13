# sandbox — Kali Execution Container

> Quick local overview. For the full, kept-current reference (the one exec path, timeouts/caps, what's agent-reachable vs not), see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the Dockerized Kali Linux environment where pentest commands run. Its default route crosses gateway policy: `block` denies external egress, `ask` opens approved destinations, and `allow` permits external destinations except unconditional/configured blocks.

## Security constraints

| Constraint | Value | Reason |
|---|---|---|
| User | `1000:1000` | Non-root execution |
| Root filesystem | Read-only | Prevent writes to the image filesystem |
| Writable mounts | `/tmp` (256 MB), `/run` (64 MB) | Tmpfs — wiped on restart |
| Container capabilities | `NET_RAW`, `NET_ADMIN` | Root PID 1 needs `NET_ADMIN` once to replace the default route; raw-packet tools need `NET_RAW`. Agent commands still run as UID 1000. |
| DNS | `1.1.1.1` / `1.0.0.1` (Cloudflare) | Same resolver the orchestrator uses, so both agree on a name's IP before the gateway opens a rule. DNS still egresses through the gateway (udp/53 accepted); resolution is not the trust boundary — the connection to the resolved IP is gated by nftables. |
| `no-new-privileges` | `false` | Required for nmap's file capabilities to be effective for UID 1000. This is an explicit hardening trade-off, not an escalation guarantee. |

## Volumes

| Host path | Container path | Access |
|---|---|---|
| `~/.yummi/wordlists` | `/wordlists` | Read-only |
| `~/.yummi/workspace` | `/workspace` | Read-write and persistent on the host |
| `~/.yummi/payloads` | `/payloads` | Read-only |

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
1. Replaces the default route with the gateway address. Compose's `depends_on` health condition ensures gateway is ready before this script starts.
2. Starts a long-lived `sleep infinity` so orchestrator can `docker exec` into it.

Commands are never run via a persistent shell — each `docker exec` call is a fresh, scoped subprocess.

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
