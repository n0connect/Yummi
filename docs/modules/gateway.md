<- [Back to README](../../README.md) · [Docs index](../README.md)

# Module: gateway (Rust)

For *why* gateway exists as a separate, minimal, memory-safe process, see [Architecture § Why separate processes](../architecture.md#why-separate-processes). For the *behavior* of everything it enforces, see [Security Model](../security-model.md). This page is the module map: which file owns which concern.

`gateway/Cargo.toml` — edition 2021, binary name `yummi-gateway` (`gateway/src/main.rs`). Built via `make _build-gateway` (native) or as part of `make start`/`docker-build` (containerized — the only path where its enforcement is real, see [Installation § Platform support](../installation.md#platform-support)).

## Table of contents

- [Module map](#module-map)
- [Startup sequence](#startup-sequence)
- [The IPC protocol, at a glance](#ipc-protocol)

---

## Module map <a id="module-map"></a>

| Module | Owns |
|---|---|
| **`policy/`** | `mod.rs` — `PolicyEngine` (RwLock-protected rule state, TTL expiry, `is_always_blocked()` floor, `default_allow` switch); `rules.rs` — `PolicyRule`, `RuleAction`, `EgressMode`, `RuleSource`; `hostname.rs` — name-level allow/block glob matching, consulted by the orchestrator before DNS resolution |
| **`ipc/`** | `mod.rs` — request dispatch (`check_scope`, `check_hostname`, `get_hostname_policy`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`/`get_egress_mode`, `list_rules`, `get_audit_log`, `add_manual_rule`); applies the configured `socket_mode` permission bits to the Unix socket right after bind (defense-in-depth — write ops are separately authorized by `SO_PEERCRED` UID regardless of socket mode); `protocol.rs` — the wire types, mirrored on the Go side by `orchestrator/internal/gateway/protocol.go`. Length-prefixed JSON framing (`[4-byte BE length][JSON body]`) |
| **`network/`** | `nftables.rs` — the **only** concrete network controller (Linux/`nf_tables`-only by design; the old cross-platform trait and macOS no-op stub were removed — `mod.rs` says so explicitly). Owns the `FORWARD`/NAT chain build, TTL normalization, MAC randomization, and the blanket-accept rule inserted only in `allow` egress mode |
| **`audit/`** | `mod.rs` — the SHA-256 hash-chained SQLite WAL audit log; `schema.sql` — its schema. See [Security Model § Audit](../security-model.md#layer-audit) |
| **`observer/`** | Best-effort `/proc/net/{tcp,udp,tcp6}` diagnostic for sockets visible in gateway's network namespace. Forwarded packets do not necessarily create local socket rows, so this is not an enforcement or complete traffic-observation mechanism |
| **`watchdog.rs`** | Writes a heartbeat file and enforces an RSS memory ceiling; on breach it flushes audit state, removes the heartbeat, and exits. See [Security Model § The watchdog](../security-model.md#watchdog) |
| **`config.rs`** | Parses `gateway/config/default.yaml` — `ScopeConfig` (allow/block/hostname_allow/hostname_block), `policy.default_allow`, `network.*` (fingerprint masking), `watchdog.*`, `ipc.*` |

## Startup sequence <a id="startup-sequence"></a>

The order genuinely matters and is worth stating explicitly, since it's the concrete mechanism behind the fail-closed startup claim in [Security Model](../security-model.md#startup-ordering):

```
1. Load config (gateway/config/default.yaml, or --config path)
2. Install nftables DROP-all rules for the sandbox source IP  <- before step 3
3. Spawn the policy-TTL cleanup, observer, IPC-server, and watchdog tasks
4. The IPC task binds the Unix socket; the watchdog begins heartbeat/RSS checks
```

Step 2 happening before step 3 is the whole point — there is no window where gateway can receive an IPC request but hasn't yet locked down traffic.

## The IPC protocol, at a glance <a id="ipc-protocol"></a>

| Request | Purpose |
|---|---|
| `check_scope` | Legacy-shaped scope check; internally routes to `check_egress`/`check_egress_unresolved` depending on whether the target parses as an IP |
| `check_hostname` | Name-level policy verdict, before any DNS resolution happens |
| `get_hostname_policy` | Read the configured `hostname_allow`/`hostname_block` glob lists |
| `add_hostname_rule` / `remove_hostname_rule` | Add/remove a pattern from the hostname allow/block list **at runtime** — in-memory only, resets to the config file's lists on restart (same non-persistence contract as `set_egress_mode`) |
| `get_status` / `ping` | Liveness/status (egress mode has its own request) |
| `add_temporary_rule` / `remove_rule` | Open/close an agent-sourced TTL allow rule |
| `set_egress_mode` / `get_egress_mode` | The runtime Block/Allow/Ask switch — see [Security Model § Egress modes](../security-model.md#egress-modes) |
| `list_rules` | Every active rule, for the Gateway tab's table |
| `get_audit_log` | Recent audit rows, for the Gateway tab's log view |
| `add_manual_rule` | Operator-added rule from the UI, independent of any approval flow |
| `get_fingerprint_config` | Read TTL-normalization and MAC-randomization settings |

Every one of these is mirrored on the Go side by `orchestrator/internal/gateway/client.go` and `protocol.go` — if you add a new IPC request type, both `gateway/src/ipc/protocol.rs` and the Go client need the matching addition, or orchestrator simply has no way to construct the request.
