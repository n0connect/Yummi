# gateway — Trust Boundary

gateway (`p*`) is the Rust daemon that enforces fail-closed network isolation between the Kali execution container (sandbox) and the external network. It is the only component that can permit outbound traffic from sandbox.

## Design principle

**Fail-closed by default.** On startup, gateway installs DROP-all nftables rules. sandbox has no external connectivity until gateway explicitly allows it. If gateway crashes or loses its IPC connection, the DROP rules remain in place — sandbox cannot exfiltrate data or reach unauthorized hosts.

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
    mod.rs      NetworkController trait
    nftables.rs Linux: nftables via nft CLI (fail-closed DROP default)
    mock.rs     macOS/test: no-op controller
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

**Request types:**

| Method | Payload | Returns |
|---|---|---|
| `check_scope` | `{"target": "1.2.3.4"}` | `{"allowed": true}` |
| `get_status` | `{}` | `{"level": 0, "rules_active": true, "uptime_secs": 42}` |
| `add_rule` | `{"target": "...", "action": "allow", "ttl_secs": 300}` | `{"ok": true}` |

## Policy engine

Rules are evaluated in order: Block rules take precedence over Allow rules. Rules with a TTL expire automatically. The engine holds a `RwLock` so reads (scope checks) never block each other.

```
PENTEST_SCOPE_ALLOW=192.168.1.0/24,example.com
PENTEST_SCOPE_BLOCK=192.168.1.1,admin.example.com
```

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

- **Linux**: Full nftables enforcement. Requires `NET_ADMIN` capability.
- **macOS**: Mock controller — no actual traffic rules. For development only.

## Watchdog

The watchdog monitors:
- RSS memory against a configurable limit (default: 256 MB)
- Heartbeat file freshness
- On threshold breach → emergency lockdown (all traffic blocked, daemon exits)
