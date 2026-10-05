[Back to Project README](../README.md) · [Documentation Index](README.md)

# Security Model & Threat Boundaries

Yummi's security architecture enforces defense-in-depth against both external network compromise and untrusted model behaviors (e.g. prompt injection, data exfiltration payloads, and unintended command generation).

This document specifies the guarantees, implementation mechanisms, and operational boundaries across the three core defense layers: Network, Content, and Audit.

## Table of Contents

- [The Core Invariant: Fail-Closed Policy](#fail-closed)
- [Layer 1: Network Boundary (Rust Gateway)](#layer-network)
  - [Deterministic Initialization Order](#startup-ordering)
  - [Unconditional Address Floor](#always-blocked-floor)
  - [Runtime Egress Modes (4-Way Gateway Switch)](#egress-modes)
  - [Pre-Resolution Hostname Policy](#hostname-policy)
  - [Rule Provenance and Precedence](#rule-sources)
- [Layer 2: Content Boundary (`threatscan`)](#layer-content)
  - [Threat Model and Rationale](#why-content-layer)
  - [Three Inspection Scopes](#threatscan-scopes)
  - [Enforcement Points in Execution Pipeline](#threatscan-call-sites)
  - [Single-Command Result Review](#threatscan-single-command)
- [Layer 3: Tamper-Evident Audit Logging](#layer-audit)
- [Container Isolation and Sandboxing](#container-isolation)
  - [Unified Sandbox Architecture](#unified-sandbox)
- [Fingerprint Minimization](#fingerprint-masking)
- [Watchdog Subsystem](#watchdog)
- [Explicit Limitations and Residual Risks](#honest-limits)

---

## The Core Invariant: Fail-Closed Policy <a id="fail-closed"></a>

For every routing and execution decision: **Any missing, malformed, or unrecognized policy verdict results in an immediate DENY.**

The runtime enforces this contract through four synchronized controls:
1. **Firewall Priority**: Gateway installs kernel DROP-all `nftables` rules before binding its control socket.
2. **Network Isolation**: The sandbox container belongs exclusively to an isolated Docker network (`yummi_internal`, `internal: true`) and routes external traffic strictly through the gateway IP (`172.20.0.2`).
3. **Volatile Permissive State**: Egress mode defaults to `block` and strictly resets to `block` on every gateway daemon restart.
4. **Defensive API Decoders**: The Go orchestrator maps unknown verdict responses to hard execution blocks.

> [!NOTE]
> Process crashes retain previously committed kernel `nftables` rules until the container namespace is destroyed or reinitialized. Startup re-establishes the default fail-closed state.

---

## Layer 1: Network Boundary (Rust Gateway) <a id="layer-network"></a>

### Deterministic Initialization Order <a id="startup-ordering"></a>

Gateway initializes in strict sequential order:
1. Load scope and rule configuration (`config/default.yaml`).
2. Program kernel `FORWARD` and NAT rules in `nftables` for the sandbox IP (`172.20.0.10`).
3. Spawn background TTL expiration and observer workers.
4. Bind and listen on Unix domain socket (`/run/yummi/gateway.sock`).

This sequence guarantees no execution request can arrive before network isolation is established.

### Unconditional Address Floor <a id="always-blocked-floor"></a>

Prior to consulting allow or block rules, `is_always_blocked()` unconditionally rejects:
- IPv4/IPv6 loopback addresses (`127.0.0.0/8`, `::1`).
- Link-local addresses (`169.254.0.0/16`, `fe80::/10`), including cloud instance metadata endpoints (`169.254.169.254`).
- Multicast and unspecified ranges (`224.0.0.0/4`, `0.0.0.0`, `::`).

Private RFC 1918 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) are populated in `scope.block` by default, but remain configurable for internal engagements.

### Runtime Egress Modes (4-Way Gateway Switch) <a id="egress-modes"></a>

Operators control external egress via a 4-way master switch (`EgressMode`):

| Mode | Wire Values / Aliases | Behavior | Operational Use Case |
|---|---|---|---|
| **`block_all`** (Default) | `block_all`, `block` | Denies all outbound traffic unconditionally, ignoring active allow rules. Fail-closed. | Complete airgap; offline analysis and local tooling where zero egress is tolerated. |
| **`block_all_except_rules`** | `block_all_except_rules`, `rules_only` | Denies outbound traffic by default, but permits destinations explicitly matched by configured scope or active temporary allow rules. | Strict assessment scopes with explicit CIDR and host authorizations. |
| **`allow_all`** | `allow_all`, `allow` | Permits outbound traffic to all public destinations not explicitly blocked. | Broad reconnaissance and public targets without pre-resolving individual IPs. |
| **`ask_every_time`** | `ask_every_time`, `ask` | Holds new destination requests in `awaiting_egress_approval` until interactive operator confirmation. | Controlled testing requiring per-target visibility and manual sign-off. |

> [!IMPORTANT]
> Egress mode resets to **`block_all`** on every gateway daemon restart (fail-closed by design).
> Egress mode is independent of Agent Approval Mode:
> - **Egress Mode (Gateway)** controls packet-layer network reachability.
> - **Approval Mode (Orchestrator)** controls command execution authorization (`auto` vs. `ask_permission`).

### Pre-Resolution Hostname Policy <a id="hostname-policy"></a>

Because packet filters operate on IP headers, domain-level restrictions (`scope.hostname_allow`, `scope.hostname_block`) are enforced by the orchestrator prior to DNS resolution:

```text
Decision Precedence:
1. Glob match in hostname_block  -> BLOCK (Denies connection; overrides Allow egress mode)
2. Non-empty hostname_allow      -> Must match glob (Whitelist), else BLOCK
3. Default                       -> NEUTRAL (Defers decision to IP egress mode)
```

Wildcards (`*.example.com`) cover both apex domains and arbitrary subdomains. Evaluated domain blocks are recorded in the tamper-evident audit log.

### Rule Provenance and Precedence <a id="rule-sources"></a>

Policy rules carry provenance metadata:
- `Config`: Loaded from static YAML at startup.
- `Agent`: Opened dynamically by the executor for approved commands (bounded by TTL).
- `Manual`: Added directly by the operator via the Gateway UI console.

Precedence rules prevent agent-generated rules from overriding configured block rules. Manual rules can carve out specific subnets from broader blocks only if they specify a more specific prefix.

---

## Layer 2: Content Boundary (`threatscan`) <a id="layer-content"></a>

### Threat Model and Rationale <a id="why-content-layer"></a>

Network firewalls cannot prevent indirect prompt injection or payload smuggling embedded within legitimate tool output (e.g. malicious HTML comments, simulated system messages, C2 instructions, or invisible Unicode sequences).

`orchestrator/internal/security/threatscan` provides content-layer inspection on all data entering the system from external sources before it reaches model context.

### Three Inspection Scopes <a id="threatscan-scopes"></a>

| Scope | Patterns Detected | Enforcement Target | Policy Action on Match |
|---|---|---|---|
| **`all`** | Common prompt injection, hidden HTML comments/tags, secret exfiltration pipes. | Baseline text checks. | Warning / Logged. |
| **`context`** | Role-hijacking phrasing, C2 framework terminology (Cobalt Strike, Sliver, etc.), prompt injection. | Command stdout/stderr and sub-agent summaries. | Pauses task at `threat_hold` node; awaits operator review. |
| **`strict`** | Backdoor signatures (`authorized_keys`), credentials, agent configuration overrides. | Persistent memory writes (`REMEMBER`) and skill YAMLs. | Hard block; rejects write. |

> [!NOTE]
> ANSI escape codes and terminal formatting sequences are stripped prior to pattern evaluation (`StripANSI`) to prevent evasion via split characters.

### Enforcement Points in Execution Pipeline <a id="threatscan-call-sites"></a>

1. **Tool Observations**: Output from sandbox commands is scanned (`ScopeContext`) before insertion into the graph observation history.
2. **Chat Command Feedback**: Output from ad-hoc chat executions is scanned at the execution store chokepoint before returning to the conductor.
3. **Memory Ingestion**: `REMEMBER` writes pass through `ScopeStrict` validation before persisting to SQLite.
4. **Sub-Agent Summaries**: Delegation results are scanned before merging into parent task context.
5. **Skill Registry**: YAML playbook definitions are verified with `ScopeStrict` during load time.

### Single-Command Result Review <a id="threatscan-single-command"></a>

When single-command execution output triggers a threat pattern, status moves to `awaiting_threat_approval`. The operator inspects the raw output and explicitly chooses whether to release sanitized results into the conductor context or suppress model feedback while retaining the audit record.

---

## Layer 3: Tamper-Evident Audit Logging <a id="layer-audit"></a>

All scope queries, rule modifications, and egress policy changes are logged to SQLite in WAL mode (`gateway/src/audit/`).

Each log record includes a cryptographic SHA-256 hash that incorporates the hash of the preceding entry:

$$\text{Hash}_n = \text{SHA256}(\text{Record}_n \mathbin{\Vert} \text{Hash}_{n-1})$$

Any modification, deletion, or insertion of historical log entries invalidates the forward hash chain, providing verifiable audit integrity.

---

## Container Isolation and Sandboxing <a id="container-isolation"></a>

Container execution is constrained through multiple isolation layers:
- Non-root user execution (`UID 1000`, `pentester`).
- `docker-proxy` restriction allowing only container exec and inspect calls.
- Linux capability bounds (`NET_RAW` for raw packet tools, minimal file capabilities).

### Unified Sandbox Architecture <a id="unified-sandbox"></a>

The sandbox runs as a unified Kali container serving three operator interfaces:
1. Agent autonomous command execution via `docker exec`.
2. Interactive Web Terminal via `tmux` and WebSocket (`/ws/terminal`).
3. Graphical Desktop session via XFCE, TigerVNC, and noVNC (`/ws/desktop`).

**Design Considerations**:
- **Writable Root Filesystem**: Required to support XFCE desktop state, browser profiles, and security tool configurations with zero environment drift between agent and operator.
- **Localhost-Bound Access**: The noVNC bridge binds strictly to localhost (`127.0.0.1:9080`), validates WebSocket `Origin` headers, and avoids publishing raw VNC ports to the host interface.

---

## Fingerprint Minimization <a id="fingerprint-masking"></a>

Optional network controls reduce sandbox fingerprinting during sensitive engagements:
- **`ttl_normalize`**: Rewrites outbound packet IP TTL to a standard value (e.g., 64) to neutralize OS fingerprinting tools (`nmap -O`, `p0f`).
- **`mac_randomization`**: Generates a pseudo-random locally-administered MAC address on the gateway egress interface at startup.

*Non-Goal*: Layer 7 HTTP header anonymization is explicitly excluded to preserve gateway simplicity and fail-closed auditability.

---

## Watchdog Subsystem <a id="watchdog"></a>

The gateway watchdog monitors resource limits and process health:
- Writes periodic timestamps to `/run/yummi/gateway.heartbeat`.
- Enforces an RSS memory limit (`watchdog.max_memory_mb`, default 512 MB).
- Upon ceiling violation, flushes the audit WAL log, removes the heartbeat file, and safely terminates the process without removing active packet rules.

---

## Explicit Limitations and Residual Risks <a id="honest-limits"></a>

- **Container Root Boundary**: While agent commands run as UID 1000, container capabilities (`NET_ADMIN`) exist at the container level. Sandbox root compromise could theoretically modify container routing tables.
- **Single-Operator Trust Model**: Desktop and terminal WebSocket endpoints rely on loopback binding and origin checks. Deployments accessible over shared networks must provide external authentication.
- **Dynamic CDN Resolution Window**: If a target domain resolves to different IP addresses across short TTL windows, rapid re-resolution could require re-approval in `ask` mode.
- **Pattern-Based Threat Scanning**: Content scanning mitigates structured injection patterns; it does not guarantee detection of novel semantic jailbreaks. Human review remains authoritative.
