# sandbox — Kali Execution Container

> Quick local overview. For the full, kept-current reference (the one exec path, timeouts/caps, what's agent-reachable vs not), see [docs/agent/sandbox-execution.md](../docs/agent/sandbox-execution.md).

sandbox is the Dockerized Kali Linux environment where all pentest commands run. It is isolated from the external network by the gateway trust boundary — it can only reach targets that gateway has explicitly allowed.

## Security constraints

| Constraint | Value | Reason |
|---|---|---|
| User | `1000:1000` | Non-root execution |
| Filesystem | Read-only | Prevent persistence across runs |
| Writable mounts | `/tmp` (256 MB), `/run` (64 MB) | Tmpfs — wiped on restart |
| Capabilities | `NET_RAW`, `NET_ADMIN` | Required for nmap, tcpdump |
| DNS | `1.1.1.1` / `1.0.0.1` (Cloudflare) | Same resolver the orchestrator uses, so both agree on a name's IP before the gateway opens a rule. DNS still egresses through the gateway (udp/53 accepted); resolution is not the trust boundary — the connection to the resolved IP is gated by nftables. |
| `no-new-privileges` | `true` | No privilege escalation |

## Volumes

| Host path | Container path | Access |
|---|---|---|
| `~/.yummi/wordlists` | `/wordlists` | Read-only |
| `~/.yummi/workspace` | `/workspace` | Read-write |
| `~/.yummi/payloads` | `/payloads` | Read-only |

## Adding a tool

Edit `sandbox/Dockerfile`. Tools must be installed from the Kali rolling repository:

```dockerfile
RUN apt-get install -y --no-install-recommends \
    mytool \
    && apt-get clean && rm -rf /var/lib/apt/lists/*
```

There is **no tool allowlist** to register the tool in. The model relies on its own pretrained knowledge to use it; for a niche tool, add a short usage note to `persona/v0.1/core_identity.yaml` or `agent.yaml` instead. The validator gates on *risk*, not on tool identity.

## Entrypoint

`entrypoint.sh` runs as the container's PID 1. It:
1. Waits for gateway to become healthy (socket present)
2. Starts a long-lived `sleep infinity` so orchestrator can `docker exec` into it

Commands are never run via a persistent shell — each `docker exec` call is a fresh, scoped subprocess.

## Healthcheck

```yaml
test: ["CMD", "/bin/bash", "-lc", "nmap --version >/dev/null 2>&1"]
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

All traffic from sandbox to external hosts is intercepted by gateway. Bypassing gateway from sandbox is not possible without `CAP_NET_ADMIN` and direct nftables access, which sandbox does not have.
