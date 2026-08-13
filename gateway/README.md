# gateway — Trust Boundary

> Quick local overview. For the full, kept-current reference (module map, IPC protocol, startup sequence), see [docs/modules/gateway.md](../docs/modules/gateway.md); for the enforcement behavior itself, see [docs/security-model.md](../docs/security-model.md).

gateway is the Rust daemon that installs and manages the nftables policy between the Kali execution container (sandbox) and external networks. Agent/direct-exec traffic is opened through its IPC API; an operator can also select blanket `allow` mode or add manual rules.

## Design principle

**Fail-closed by default.** On startup, gateway installs its nftables rules before spawning the IPC server. A normal daemon crash does not run cleanup, so those rules remain in the gateway container's network namespace; an explicit `--cleanup`, container removal, host/network reconfiguration, or a sandbox root compromise is outside that narrower guarantee.

```text
   gateway starts ──▶ install DROP-all nftables rules ──▶ open IPC socket
                                  │                              │
                                  │  (if the gateway dies, the   │  add_temporary_rule
                                  │   DROP rules persist) ───┐   ▼
                                  ▼                          │  evaluate target
                              ⛔ DROP  ◀────────────────────┴── always-blocked floor
                              (always wins)                      (loopback · link-local
                                  ▲                               · multicast · unspecified)
              Block rule matches  │                              │
              ────────────────────┘                              │ Allow rule + egress mode
                                                                 ▼
                                                          ✅ forward + audit
```

The always-blocked floor (`is_always_blocked`) sits before every egress mode. It covers IPv4/IPv6 loopback, link-local, multicast, and unspecified addresses, including the link-local cloud-metadata address `169.254.169.254`. RFC1918 private ranges are not part of this unconditional floor; they are controlled by configured block rules and narrowly scoped manual overrides.

## Package layout

```
src/
  main.rs       Entry point: CLI flags, config load, tokio runtime
  lib.rs        Public re-exports
  config.rs     YAML config loader (scope, policy, watchdog params)
  watchdog.rs   RSS memory ceiling + heartbeat writer
  policy/
    mod.rs      PolicyEngine — RwLock-protected allow/block state with TTL expiry
    rules.rs    IP/CIDR rule types, egress modes, and rule-source tags
    hostname.rs Runtime hostname allow/block glob policy
  ipc/
    mod.rs      Unix domain socket server (tokio), connection semaphore
    protocol.rs Length-prefixed JSON request/response framing
  network/
    mod.rs      Traffic metadata types (the cross-platform trait + macOS mock were removed)
    nftables.rs Linux/nftables-only controller — FORWARD chain, egress modes, fingerprint masking
  observer/
    mod.rs      Best-effort /proc/net diagnostic observer for the gateway namespace
    capture.rs  Packet-metadata value types and TCP flag formatting
  audit/
    mod.rs      SQLite WAL audit log with SHA-256 hash chain
config/
  default.yaml  Default scope and policy configuration
```

## IPC protocol

orchestrator communicates with gateway over a Unix domain socket (`/run/yummi/gateway.sock`) using a custom length-prefixed JSON request/response protocol (not JSON-RPC):

```
[ 4-byte big-endian length ][ JSON request body ]
[ 4-byte big-endian length ][ JSON response body ]
```

**Request types** (`src/ipc/protocol.rs`):

| Method | Purpose |
|---|---|
| `check_scope` | Resolve an IP target to a `verdict` (`allow` / `block` / `needs_approval`) |
| `check_hostname` | Name-level verdict (`allow` / `block` / `neutral`) against the configured hostname patterns |
| `get_hostname_policy` | Configured plus runtime hostname allow/block patterns |
| `add_hostname_rule` / `remove_hostname_rule` | Edit hostname patterns in memory; resets on restart |
| `get_status` | Level, rules-active, uptime |
| `ping` | Liveness |
| `add_temporary_rule` | Add an agent-sourced TTL allow rule (`{network, ttl_secs, description?}`) |
| `add_manual_rule` | Add an operator-sourced TTL allow rule (`{network, ttl_secs, reason}`) |
| `remove_rule` | Drop a rule by id |
| `list_rules` | Active rules (with `source: config\|agent\|manual`) |
| `set_egress_mode` / `get_egress_mode` | Read/write the runtime egress master switch |
| `get_audit_log` | Recent tamper-evident audit entries |
| `get_fingerprint_config` | Current fingerprint-masking config |

## Policy engine

Rules with a TTL expire automatically. Configured block rules normally beat allow rules; an operator-created manual allow may carve out an address only when it is at least as specific as the matching configured block. Agent-created temporary rules cannot override configured blocks. The unconditional address-class floor wins in all modes.

Network scope is configured in the gateway's own YAML config (parsed by `src/config.rs` `ScopeConfig`) — the gateway is the sole scope authority:

```yaml
scope:
  allow:
    - "192.168.1.0/24"
  block:
    - "192.168.1.1"
    - "127.0.0.0/8"
  # Name-level scope (see below). Wildcard "*.example.com" covers the apex
  # AND any subdomain at any depth.
  hostname_allow:
    - "*.acme-corp.com"   # whitelist: only matching names may proceed
  hostname_block:
    - "*.facebook.com"    # hard deny by name, wins over everything
```

### Hostname policy

The gateway only sees IPs in packets, so name-level scope is enforced by the
orchestrator: before it resolves a hostname it calls `check_hostname`, and the
gateway returns a verdict against `hostname_allow`/`hostname_block`:

- a `hostname_block` match → **block** (hard, name-level deny — wins even in `allow` egress mode);
- else if `hostname_allow` is non-empty it is a **whitelist** (a name must match or it's blocked);
- else → **neutral** (no name restriction; the IP-egress flow decides).

`*.example.com` matches `example.com` and any subdomain (`a.b.example.com`); a
pattern with no `*` is an exact match. A name-level block is recorded in the
tamper-evident audit chain (`reason: hostname_blocked`). View and edit the
combined config/runtime lists through `GET`/`POST`/`DELETE` on
`/api/v1/gateway/hostname-policy`. Runtime edits reset on gateway restart.

### Egress mode

Above the per-IP scope list sits a runtime **egress mode** — an operator-controlled master switch (set from the dashboard GATEWAY tab via the `set_egress_mode` IPC / `POST /api/v1/gateway/egress-mode`):

- `block` — **default, fail-closed**: no external egress at all.
- `allow` — the FORWARD chain blanket-accepts sandbox egress; hostnames (e.g. `google.com`) work without per-IP resolution.
- `ask` — each new external destination requires operator approval.

`check_scope` returns a `verdict` (`allow` / `block` / `needs_approval`) for an IP. The wire handler also accepts a non-IP string and applies the mode-only unresolved-target verdict, although production executor code performs hostname policy and DNS resolution before IP egress checks. The mode is **not persisted** — it resets to `block` on restart. It is independent of the agent's command-approval mode (`auto`/`ask_permission`).

## Audit log

Scope checks, hostname blocks, rule/mode changes, and observer decisions are written to the configured SQLite WAL database (default `/var/lib/yummi/gateway/audit.db`). Each row includes a SHA-256 hash of the previous row, making later modification detectable when the chain is verified.

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
cargo test test_allow_in_scope
```

## Platform notes

- **Linux**: full nftables enforcement. Requires the `NET_ADMIN` capability.
- **macOS via Docker Desktop** (`make start`, the real product path): gateway runs as a container on Docker Desktop's Linux VM, which is a genuine Linux kernel with `nf_tables` support — nftables enforcement works normally. Verified end-to-end: FORWARD-chain DROP-all + a single-target Allow rule correctly let through an allowed destination and dropped a blocked one.
- **Raw binary on macOS**: the entry point returns an explicit Linux-only error before enforcement starts. Use the containerized Linux path for real operation.

## Watchdog

The watchdog writes a timestamp heartbeat file every configured interval and checks the gateway process RSS against `watchdog.max_memory_mb` (shipped config `512`, code fallback `256`). It does not inspect heartbeat freshness itself. On an RSS breach it flushes the audit log, removes the heartbeat file, and exits; it does not install a second emergency ruleset, so fail-closed behavior relies on the nftables rules already installed at startup.
