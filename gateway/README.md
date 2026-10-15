# gateway — Trust Boundary

> For the full, kept-current reference, see [docs/modules/gateway.md](../docs/modules/gateway.md) and [docs/security-model.md](../docs/security-model.md).

gateway is the Rust daemon that installs and manages the `nftables` policy between the Kali execution container (sandbox) and external networks. Agent/direct-exec traffic is opened through its IPC API; an operator can also select blanket `allow` mode or add manual rules.

---

## Design Principle

**Fail-closed by default.** On startup, gateway installs its `nftables` rules before spawning the IPC server. A normal daemon crash does not run cleanup, so those rules remain in the gateway container's network namespace.

```text
   gateway starts ---> install DROP-all nftables rules ---> open IPC socket
                                  |                              |
                                  |  (if the gateway dies, the   |  add_temporary_rule
                                  |   DROP rules persist) ---+   v
                                  v                          |  evaluate target
                                 DROP  <---------------------+-- always-blocked floor
                             (Fail-closed)                       (loopback / link-local
                                  ^                               / multicast / unspecified)
              Block rule matches  |                              |
              --------------------+                              | Allow rule + egress mode
                                                                 v
                                                          FORWARD + AUDIT
```

The always-blocked floor (`is_always_blocked`) sits before every egress mode. It covers IPv4/IPv6 loopback, link-local, multicast, and unspecified addresses, including the link-local cloud-metadata address `169.254.169.254`. RFC1918 private ranges are not part of this unconditional floor; they are controlled by configured block rules and narrowly scoped manual overrides.

---

## Package Layout

```text
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
    mod.rs      Traffic metadata types
    nftables.rs Linux/nftables controller — FORWARD chain, egress modes, fingerprint masking
  observer/
    mod.rs      Best-effort /proc/net diagnostic observer for the gateway namespace
    capture.rs  Packet-metadata value types and TCP flag formatting
  audit/
    mod.rs      SQLite WAL audit log with SHA-256 hash chain
config/
  default.yaml  Default scope and policy configuration
```

---

## IPC Protocol

The orchestrator communicates with gateway over a Unix domain socket (`/run/yummi/gateway.sock`) using a length-prefixed JSON request/response protocol:

```text
[ 4-byte big-endian length ][ JSON request body ]
[ 4-byte big-endian length ][ JSON response body ]
```

### Request Types (`src/ipc/protocol.rs`):

| Method | Purpose |
|---|---|
| `check_scope` | Resolve an IP target to a verdict (`allow` / `block` / `needs_approval`). |
| `check_hostname` | Name-level verdict (`allow` / `block` / `neutral`) against configured hostname patterns. |
| `get_hostname_policy` | Configured plus runtime hostname allow/block patterns. |
| `add_hostname_rule` / `remove_hostname_rule` | Edit hostname patterns in memory; resets on restart. |
| `get_status` | Security level, rules-active status, uptime. |
| `ping` | Liveness heartbeat. |
| `add_temporary_rule` | Add an agent-sourced TTL allow rule (`{network, ttl_secs, description?}`). |
| `add_manual_rule` | Add an operator-sourced TTL allow rule (`{network, ttl_secs, reason}`). |
| `remove_rule` | Drop a rule by ID. |
| `list_rules` | Active rules (tagged by `source: config|agent|manual`). |
| `set_egress_mode` / `get_egress_mode` | Read/write the runtime 4-way egress switch (`block_all`, `block_all_except_rules`, `allow_all`, `ask_every_time`). |
| `get_audit_log` | Recent tamper-evident audit entries. |
| `get_fingerprint_config` | Current fingerprint-masking config. |

---

## Egress Modes

The gateway enforces a 4-way master switch over the `nftables` FORWARD chain:
- **`block_all`** (`block`): Default state. Drops all outbound egress from the sandbox container, including DNS and packets on established connections, ignoring active allow rules.
- **`block_all_except_rules`** (`block_except_rules`, `rules_only`): Default-drop, but permits destinations explicitly matched by active temporary allow rules or configured CIDRs.
- **`allow_all`** (`allow`): Blanket allows outbound sandbox traffic to public destinations not explicitly blocked.
- **`ask_every_time`** (`ask`): Prompts the operator for interactive authorization on each new destination IP/hostname.

Egress mode resets to **`block_all`** on every gateway daemon restart (fail-closed by design).
UDP/53 DNS resolution is an explicit exception in the three other modes, after
configured destination blocks. Every sandbox-sourced packet rechecks current
scope, so approval expiry or removal also blocks established outbound flows.
Other containers' traffic and return traffic keep their source-based pass-through.

### Real kernel regression

The ignored Rust integration test uses the production controller and real Linux
nftables/conntrack with three routed network namespaces (sandbox, destination,
and unrelated container), once for IPv4 and once for IPv6:

```sh
docker build -f gateway/Dockerfile.kernel-test -t yummi/gateway-kernel-test:local .
docker run --rm --pull never --network none --privileged yummi/gateway-kernel-test:local
```

The disposable container needs privilege for network namespace mounts. It has no
host mounts, Docker socket, published ports or external network. Never run this
test binary on a live gateway. Checks include log-burst exhaustion, DNS lockdown,
established TCP revocation, explicit deny precedence, unrelated source traffic,
approval survival across mode changes, and kernel TTL expiry/removal of live flows.
Regular `cargo test` does not run the privileged test.

---

## Policy Engine

Rules with a TTL expire automatically. Configured block rules normally beat allow rules; an operator-created manual allow may carve out an address only when it is at least as specific as the matching configured block. Agent-created temporary rules cannot override configured blocks. The unconditional address-class floor wins in all modes.

Network scope is configured in the gateway's own YAML config (`config/default.yaml`) — the gateway is the sole scope authority:

```yaml
scope:
  allow:
    - "192.168.1.0/24"
  block:
    - "192.168.1.1"
    - "127.0.0.0/8"
  hostname_allow:
    - "*.acme-corp.com"   # Whitelist: only matching names may proceed
  hostname_block:
    - "*.facebook.com"    # Hard deny by name, overrides everything
```
