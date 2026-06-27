# gateway — Trust Boundary

gateway is the Rust daemon that enforces fail-closed network isolation between the Kali execution container (sandbox) and the external network. It is the only component that can permit outbound traffic from sandbox.

## Design principle

**Fail-closed by default.** On startup, gateway installs DROP-all nftables rules **before its IPC socket even opens**. The sandbox has no external connectivity until gateway explicitly allows it. If gateway crashes or loses its IPC connection, the DROP rules remain in the kernel — the sandbox cannot exfiltrate data or reach unauthorized hosts.

```text
   gateway starts ──▶ install DROP-all nftables rules ──▶ open IPC socket
                                  │                              │
                                  │  (if the gateway dies, the   │  add_temporary_rule
                                  │   DROP rules persist) ───┐   ▼
                                  ▼                          │  evaluate target
                              ⛔ DROP  ◀────────────────────┴── always-blocked floor
                              (always wins)                      (loopback · link-local
                                  ▲                               · 169.254 · private)
              Block rule matches  │                              │
              ────────────────────┘                              │ Allow rule + egress mode
                                                                 ▼
                                                          ✅ forward + audit
```

The always-blocked floor (`is_always_blocked`) sits **before** any egress allowance, so loopback, link-local and cloud-metadata (`169.254.169.254`) ranges are dropped even when egress mode is `allow` — closing the SSRF-to-metadata vector.

## Package layout

```
src/
  main.rs       Entry point: CLI flags, config load, tokio runtime
  lib.rs        Public re-exports
  config.rs     YAML config loader (scope, policy, watchdog params)
  watchdog.rs   Memory limit monitor + emergency lockdown + heartbeat file
  policy/
    mod.rs      PolicyEngine — RwLock<HashMap<RuleKey, Rule>> with TTL expiry
    rules.rs    Rule types: Allow/Block, target match (IP, CIDR, domain)
  ipc/
    mod.rs      Unix domain socket server (tokio), connection semaphore
    protocol.rs Length-prefixed JSON request/response framing
  network/
    mod.rs      Traffic metadata types (the cross-platform trait + macOS mock were removed)
    nftables.rs Linux/nftables-only controller — FORWARD chain, egress modes, fingerprint masking
  observer/
    mod.rs      Passive monitor stub
    capture.rs  /proc/net/{tcp,udp} reader — logs out-of-scope connections
  audit/
    mod.rs      SQLite WAL audit log with SHA-256 hash chain
config/
  default.yaml  Default scope and policy configuration
```

## IPC protocol

orchestrator communicates with gateway over a Unix domain socket (`/run/yummi/gateway.sock`) using length-prefixed JSON-RPC:

```
[ 4-byte big-endian length ][ JSON request body ]
[ 4-byte big-endian length ][ JSON response body ]
```

**Request types** (`src/ipc/protocol.rs`):

| Method | Purpose |
|---|---|
| `check_scope` | Resolve a target to a `verdict` (`allow` / `block` / `needs_approval`) |
| `get_status` | Level, rules-active, uptime |
| `ping` | Liveness |
| `add_temporary_rule` | Add a TTL'd Allow/Block rule (`{network, ttl_secs, reason}`) |
| `remove_rule` | Drop a rule by id |
| `list_rules` | Active rules (with `source: config\|agent\|manual`) |
| `set_egress_mode` / `get_egress_mode` | Read/write the runtime egress master switch |
| `get_audit_log` | Recent tamper-evident audit entries |
| `get_fingerprint` | Current fingerprint-masking config |

## Policy engine

Rules are evaluated in order: Block rules take precedence over Allow rules. Rules with a TTL expire automatically. The engine holds a `RwLock` so reads (scope checks) never block each other.

Network scope is configured in the gateway's own YAML config (parsed by `src/config.rs` `ScopeConfig`) — the gateway is the sole scope authority:

```yaml
scope:
  allow:
    - "192.168.1.0/24"
  block:
    - "192.168.1.1"
    - "127.0.0.0/8"
```

### Egress mode

Above the per-IP scope list sits a runtime **egress mode** — an operator-controlled master switch (set from the dashboard GATEWAY tab via the `set_egress_mode` IPC / `POST /api/v1/gateway/egress-mode`):

- `block` — **default, fail-closed**: no external egress at all.
- `allow` — the FORWARD chain blanket-accepts sandbox egress; hostnames (e.g. `google.com`) work without per-IP resolution.
- `ask` — each new external destination requires operator approval.

`check_scope` returns a `verdict` (`allow` / `block` / `needs_approval`) derived from the mode; a non-IP target (hostname) is decided by the mode rather than rejected. The mode is **not persisted** — it resets to `block` on restart. It is independent of the agent's command-approval mode (`auto`/`ask_permission`).

## Audit log

Every scope check, rule change, and connection event is written to `~/.yummi/logs/audit.db` (SQLite WAL). Each row includes a SHA-256 hash of the previous row — creating a tamper-evident chain.

## Build & test

```bash
# From repo root
make _build-gateway    # release build
make _test-gateway     # cargo test

# Direct
cd gateway
cargo build --release
cargo test

# Single test
cargo test policy_engine::tests::test_allow_rule
```

## Platform notes

- **Linux**: full nftables enforcement. Requires the `NET_ADMIN` capability.
- **macOS**: the crate *compiles* (it shells out to the `nft` CLI), so `make _build-gateway` works — but enforcement **fails at runtime** off Linux. Local macOS dev is **not** a real isolation boundary.

## Watchdog

The watchdog monitors:
- RSS memory against a configurable limit (`watchdog.max_memory_mb`; shipped config `512`, code fallback `256`)
- Heartbeat file freshness (`watchdog.heartbeat_interval_secs`, default `5`)
- On a breach → **emergency lockdown** (all traffic blocked) and daemon exit — fail-closed to the end.
