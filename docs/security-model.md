[Back to Project README](../README.md) · [Documentation Index](README.md)

# Security Model & Threat Boundaries

Yummi's security architecture enforces defense-in-depth against both external network compromise and untrusted model behaviors (e.g. prompt injection, data exfiltration payloads, and unintended command generation).

This document specifies the guarantees, implementation mechanisms, and operational boundaries across network policy, content review, audit logging, and execution provenance.

## Table of Contents

- [The Core Invariant: Fail-Closed Policy](#fail-closed)
- [Layer 1: Network Boundary (Rust Gateway)](#layer-network)
  - [Deterministic Initialization Order](#startup-ordering)
  - [Unconditional Address Floor](#always-blocked-floor)
  - [Runtime Egress Modes (4-Way Gateway Switch)](#egress-modes)
  - [Pre-Resolution Hostname Policy](#hostname-policy)
  - [In-Sandbox Browser & WebSocket Proxy](#browser-proxy)
  - [Rule Provenance and Precedence](#rule-sources)
- [Layer 2: Content Boundary (`threatscan`)](#layer-content)
  - [Threat Model and Rationale](#why-content-layer)
  - [Three Inspection Scopes](#threatscan-scopes)
  - [Enforcement Points in Execution Pipeline](#threatscan-call-sites)
  - [Untrusted Web Research Containment](#web-research-containment)
  - [Single-Command Result Review](#threatscan-single-command)
- [Layer 3: Tamper-Evident Audit Logging](#layer-audit)
- [Layer 4: Execution Provenance & Integrity](#layer-provenance)
- [Container Isolation and Sandboxing](#container-isolation)
  - [Unified Sandbox Architecture](#unified-sandbox)
- [Fingerprint Minimization](#fingerprint-masking)
- [Watchdog Subsystem](#watchdog)
- [Explicit Limitations and Residual Risks](#honest-limits)

---

## The Core Invariant: Fail-Closed Policy <a id="fail-closed"></a>

For gateway-mediated execution checks, missing, malformed, or unknown verdicts block execution. At startup, the gateway installs `block_all` before exposing IPC. This applies to gateway-forwarded sandbox egress; local and same-subnet connections are outside that packet boundary.

The runtime enforces this contract through four synchronized controls:
1. **Firewall Priority**: Gateway installs kernel DROP-all `nftables` rules before binding its control socket.
2. **Network Isolation**: The sandbox container belongs exclusively to an isolated Docker network (`yummi_internal`, `internal: true`) and routes external traffic strictly through the gateway IP (`172.20.0.2`).
3. **Volatile Permissive State**: Egress mode defaults to `block` and strictly resets to `block` on every gateway daemon restart.
4. **Defensive API Decoders**: The Go orchestrator maps unknown verdict responses to hard execution blocks.

> [!NOTE]
> Process crashes retain the last committed kernel `nftables` rules while the network namespace survives. A crash does not automatically replace scoped allows or `allow_all` with DROP-all. Kernel TTLs still expire, but static and blanket permissions can remain until restart or explicit lockdown. Startup re-establishes `block_all`; namespace removal or administrative `--cleanup` removes this enforcement.

---

## Layer 1: Network Boundary (Rust Gateway) <a id="layer-network"></a>

### Deterministic Initialization Order <a id="startup-ordering"></a>

Gateway initializes in strict sequential order:
1. Load scope and rule configuration (`config/default.yaml`).
2. Program kernel `FORWARD` and NAT rules in `nftables` for the sandbox IP (`172.20.0.10`).
3. Open the audit store, construct policy, and spawn TTL cleanup, firewall reconciliation, observer, IPC, and watchdog tasks.
4. Bind and listen on Unix domain socket (`/run/yummi/gateway.sock`).

The IPC listener opens only after the initial kernel rules have committed. Every 15 seconds, a reconciliation worker rebuilds the expected kernel state. If repair fails it switches policy to `block_all` and attempts kernel lockdown; a failed kernel repair is surfaced as inconsistent enforcement, not a claim that existing traffic was blocked.

### Unconditional Address Floor <a id="always-blocked-floor"></a>

In policy/API checks, `is_always_blocked()` rejects these address classes before mode/rule evaluation:
- IPv4/IPv6 loopback addresses (`127.0.0.0/8`, `::1`).
- Link-local addresses (`169.254.0.0/16`, `fe80::/10`), including cloud instance metadata endpoints (`169.254.169.254`).
- Multicast and unspecified ranges (`224.0.0.0/4`, `0.0.0.0`, `::`).

This is an API/policy floor, not an automatically generated nftables floor. The packet controller renders configured `scope.block` CIDRs; it does not copy every class above into kernel DROP rules. Arbitrary tool traffic in a permissive mode must therefore be evaluated against the actual configured kernel rules, not just an API verdict.

Private RFC 1918 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) are populated in `scope.block` by default, but remain configurable for internal engagements.

### Runtime Egress Modes (4-Way Gateway Switch) <a id="egress-modes"></a>

Operators control external egress via a 4-way master switch (`EgressMode`):

| Mode | Wire Values / Aliases | Behavior | Operational Use Case |
|---|---|---|---|
| **`block_all`** (Default) | `block_all`, `block` | Denies gateway-forwarded sandbox egress, including DNS and established connections, ignoring active allow rules. Fail-closed. | Routed egress lockdown; local tooling and private policy reads remain available. |
| **`block_all_except_rules`** | `block_all_except_rules`, `block_except_rules`, `rules_only` | Denies outbound traffic by default, but permits destinations explicitly matched by configured scope or active temporary allow rules. | Strict assessment scopes with explicit CIDR and host authorizations. |
| **`allow_all`** | `allow_all`, `allow` | Permits remaining routed destinations after configured packet blocks. | Broad connectivity without per-IP temporary allows; configured blocks still apply. |
| **`ask_every_time`** | `ask_every_time`, `ask` | Holds new destination requests in `awaiting_egress_approval` until interactive operator confirmation. | Controlled testing requiring per-target visibility and manual sign-off. |

> [!IMPORTANT]
> UDP/53 remains a gateway-routed resolution exception in the other modes, after
> configured destination blocks. It is not an exception in `block_all`. Forwarded
> sandbox packets recheck current scope. In rules-only/ask mode, expiry/removal
> revokes established flows that depended on that approval and have no other
> matching allow. `allow_all` does not require temporary approvals. Local and
> same-subnet traffic, including read-only policy requests, remains outside the
> gateway FORWARD-chain boundary.

> Egress mode resets to **`block_all`** on every gateway daemon restart (fail-closed by design).
> Egress mode is independent of Agent Approval Mode:
> - **Egress Mode (Gateway)** controls packet-layer network reachability.
> - **Approval Mode (Orchestrator)** controls command execution authorization (`auto` vs. `ask_permission`).

![The Gateway tab's own egress-mode switch, set to block_all here — this is the operator control that decides every claim on this page, not something the agent can touch](assets/screenshots/usage-guide/gateway-tab.png)

### Pre-Resolution Hostname Policy <a id="hostname-policy"></a>

Because packet filters operate on IP headers, hostname restrictions
(`scope.hostname_allow`, `scope.hostname_block`) are checked separately by the
orchestrator before resolving command targets and by sandbox research/browser
connection guards before opening non-loopback destinations:

```text
1. hostname_block match -> BLOCK (including allow_all mode)
2. Non-empty hostname_allow -> require a matching pattern, otherwise BLOCK
3. Otherwise -> NEUTRAL (defer to IP policy)
```

`*.example.com` matches the apex and arbitrary subdomains. Operator REST routes
`GET/POST/DELETE /api/v1/gateway/hostname-policy` expose/read/edit the lists;
changes are in memory and reset to YAML on restart. IPC `check_hostname` decisions
are audited; the sandbox's local matching of the read-only policy list does not
itself append a decision to the gateway audit log. Arbitrary shell tools using
literal IPs, alternate name resolution, or secondary connections do not acquire
universal hostname enforcement from these application checks.

### In-Sandbox Browser & WebSocket Proxy <a id="browser-proxy"></a>

Managed Chromium (`sandbox-mcp/browser_rod.go`) uses a sandbox-local HTTP proxy
bound to `127.0.0.1` on an ephemeral port, supplied through `--proxy-server`.
The proxy checks non-loopback hostnames against the read-only policy endpoint
before DNS/dial. It supports plain HTTP, HTTPS CONNECT, and WebSocket upgrades;
policy denial/unavailability returns 403 and connection failures return 502.
Localhost/loopback destinations are deliberately permitted for local browser
fixtures and services. Gateway remains responsible for routed IP traffic.

This guard applies to managed Chromium. It is not a global interception proxy
for Firefox, arbitrary commands, or every protocol used by tools.

### Rule Provenance and Precedence <a id="rule-sources"></a>

Policy rules carry provenance metadata:
- `Config`: Loaded from static YAML at startup.
- `Agent`: Opened dynamically by the executor for approved commands (bounded by TTL).
- `Manual`: Added directly by the operator via the Gateway UI console.

Precedence rules prevent agent-generated rules from overriding configured block rules. Manual rules can carve out specific subnets from broader blocks in policy evaluation when the manual prefix is at least as specific as the most specific matching block. This never overrides the policy address-class floor. The current packet rules place configured block DROP rules before dynamic allows, so such a manual policy override does not override that kernel block; reaching the destination requires changing the configured block and rebuilding/restarting gateway.

---

## Layer 2: Content Boundary (`threatscan`) <a id="layer-content"></a>

### Threat Model and Rationale <a id="why-content-layer"></a>

Network firewalls cannot prevent indirect prompt injection or payload smuggling embedded within legitimate tool output (e.g. malicious HTML comments, simulated system messages, C2 instructions, or invisible Unicode sequences).

`orchestrator/internal/security/threatscan` inspects supported tool-feedback, delegation, memory, and skill ingestion paths. It is a pattern-based filter, not a guarantee that every external datum or novel instruction will be detected.

### Three Inspection Scopes <a id="threatscan-scopes"></a>

| Scope | Patterns Detected | Enforcement Target | Policy Action on Match |
|---|---|---|---|
| **`all`** | Common prompt injection, hidden HTML comments/tags, secret exfiltration pipes. | Baseline text checks. | Returns matches; the caller chooses the response. |
| **`context`** | Role-hijacking phrasing, C2 framework terminology (Cobalt Strike, Sliver, etc.), prompt injection. | Command stdout/stderr and sub-agent summaries. | Pauses task at `threat_hold` node; awaits operator review. |
| **`strict`** | Backdoor signatures (`authorized_keys`), credentials, agent configuration overrides. | Persistent memory writes (`REMEMBER`) and skill YAMLs. | Hard block; rejects write. |

> [!NOTE]
> ANSI escape codes and terminal formatting sequences are stripped prior to pattern evaluation (`StripANSI`) to prevent evasion via split characters.

### Enforcement Points in Execution Pipeline <a id="threatscan-call-sites"></a>

1. **Tool Observations**: Output from sandbox commands is scanned (`ScopeContext`) before insertion into the graph observation history.
2. **Chat Command Feedback**: Single-command results pass execution-store threat review before the typed feedback path can return them to the conductor.
3. **Memory Ingestion**: `REMEMBER` writes pass through `ScopeStrict` validation before persisting to SQLite.
4. **Sub-Agent Summaries**: Delegation results are scanned before merging into parent task context.
5. **Skill Registry**: YAML playbook definitions are verified with `ScopeStrict` during load time.
6. **Untrusted Web Research Containment**: Data retrieved via `web_search` and `web_fetch` is parsed, stripped of active scripts/styles, bounded in size, and returned as bounded JSON with a `UNTRUSTED_WEB_CONTENT` notice. Retrieval itself does not insert content into permanent memory; model-authored memory writes are a separate, strictly scanned path.

### Untrusted Web Research Containment <a id="web-research-containment"></a>

External web search results and fetched HTML pages represent uncontrolled, third-party content subject to prompt injection, malicious instructions, or data exfiltration attempts. Yummi protects the model and system through:
- **Strict Sandbox Localization**: `web_search` and `web_fetch` execute solely within the sandbox container under the gateway egress policy. The host orchestrator never initiates direct web queries.
- **Structural Text Transformation**: Fetched HTML is converted to bounded plain text with active/non-content elements removed. This is text extraction, not full browser rendering or a guarantee that CSS-hidden or malicious text disappears.
- **Untrusted Content Envelope**: Research JSON carries an explicit `UNTRUSTED_WEB_CONTENT` notice instructing the model to treat source text as evidence rather than instructions.
- **No Automatic Memory Insertion**: Search/fetch handlers do not write to long-term memory. A later model-authored `REMEMBER` action remains a distinct threat-scanned write; this does not guarantee semantic exclusion of all web-derived facts.
- **Provider Disclosure**: `web_search` sends queries to the configured OpenRouter/Exa service. Sandbox placement constrains execution and egress; it does not make cloud search local or anonymous. Provider credentials/model are sandbox configuration, never tool arguments.
- **Operator Toggle Control**: Web search capability is gated by an operator-controlled switch in the UI (`GET/POST /api/v1/system/web-search`); when toggled off, search tools are disabled and rejected.

### Single-Command Result Review <a id="threatscan-single-command"></a>

When single-command execution output triggers a threat pattern, status moves to `awaiting_threat_approval`. The operator inspects the raw output and explicitly chooses whether to release sanitized results into the conductor context or suppress model feedback while retaining the audit record.

---

## Layer 3: Tamper-Evident Audit Logging <a id="layer-audit"></a>

`allow_all` policy verdicts bypass configured IP block evaluation after the address floor, while the kernel still applies configured block CIDRs. A reported API `allow` therefore does not guarantee packet reachability.

Gateway IPC scope checks, hostname decisions, rule changes, and egress-mode changes attempt to append events to SQLite in WAL mode (`gateway/src/audit/`). Observer socket diagnostics may add records too. This is not a complete packet capture: forwarded packets do not create an audit row for every connection or packet, and audit-write failures are logged without necessarily rolling back a successful policy change.

The chain hashes timestamp, source IP, destination IP, decision, reason, and previous hash (not every database column):

$$\text{Hash}_n = \text{SHA256}(\text{Record}_n \mathbin{\Vert} \text{Hash}_{n-1})$$

Verification detects changes to hashed fields and breaks in the retained chain. It does not authenticate the database against a privileged writer who can recompute the chain, prove that a deleted suffix existed without an external checkpoint, or cover unhashed columns such as ports/protocol.

---

## Layer 4: Execution Provenance & Integrity <a id="layer-provenance"></a>

To prevent untrusted or hallucinating models from forging tool execution receipts, Yummi enforces an architectural execution-history boundary:
- **Server-Owned Execution Receipts**: Step execution statuses, timestamps, error records, exit codes, and output streams are strictly owned by the orchestrator runner (`Store.ReplaceModelPlan`).
- **Blacklist of Model Structural Writes**: The model cannot backfill completed execution metadata, mark pending steps as done, or alter recorded command outputs.
- **Preservation of Progress**: When updating plans, only pending or explicitly skipped steps can be modified. Existing runner-owned progress and audit cards are immutably preserved.
- See [Execution Provenance](agent/execution-provenance.md) for detailed structural invariants.

---

## Container Isolation and Sandboxing <a id="container-isolation"></a>

Container execution is constrained through multiple isolation layers:
- Non-root user execution (`UID 1000`, `pentester`).
- A constrained Docker broker on the internal control network; it permits only
  the configured sandbox, UID/GID `1000:1000` and issued exec IDs. Sandbox cannot
  reach this broker. General Docker container/exec prefix access is not granted.
- Operator API socket-peer authorization and a separate read-only sandbox
  policy listener. Published host loopback ports terminate at UI nginx on a
  UI-only ingress bridge; the orchestrator has no published port. See
  [network topology](architecture.md#network-topology).
- A bounded capability set, including `NET_RAW`, `NET_ADMIN`, and filesystem/privilege-drop capabilities. Selected binaries have file capabilities, and `no-new-privileges` is explicitly false. UID 1000 alone does not make this a hardened boundary against malicious privileged tools.

### Unified Sandbox Architecture <a id="unified-sandbox"></a>

The sandbox runs as a unified Kali container serving three operator interfaces:
1. Agent autonomous command execution via `docker exec`.
2. Interactive Web Terminal via `tmux` and WebSocket (`/ws/terminal`).
3. Graphical Desktop session via XFCE, TigerVNC, and noVNC (`/ws/desktop`).

**Design Considerations**:
- **Writable Root Filesystem**: Required to support XFCE desktop state, browser profiles, and security tool configurations with zero environment drift between agent and operator.
- **Localhost-Published Access**: Host loopback ports 3000/9080 terminate at UI nginx,
  which forwards the authorized desktop WebSocket to the orchestrator. The
  orchestrator validates `Origin` and socket peers; raw VNC ports are not published.

---

## Fingerprint Minimization <a id="fingerprint-masking"></a>

Optional network controls reduce sandbox fingerprinting during sensitive engagements:
- **`ttl_normalize`**: Rewrites outbound IPv4 TTL (or IPv6 hop limit for an IPv6 sandbox source) to a configured value (e.g., 64), reducing one fingerprint signal. It does not neutralize the many other signals used by `nmap -O` or `p0f`.
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

- **Container/Capability Boundary**: Root compromise and tools with file-granted `NET_ADMIN` can affect sandbox networking; the source-IP-based gateway filter is not a boundary against every routing, spoofing, or container-escape attack. Same-subnet traffic and the mounted host workspace also remain available independently of external egress mode.
- **Single-Operator Trust Model**: Host loopback publication, exact socket-peer/Host authorization, and origin checks protect the operator routes. There is no multi-user authentication/RBAC layer. VNC is passwordless on the private sandbox network; raw VNC is not host-published. Shared-network exposure requires a separately designed access boundary.
- **Dynamic CDN Resolution Window**: If a target domain resolves to different IP addresses across short TTL windows, rapid re-resolution could require re-approval in `ask` mode.
- **Pattern-Based Threat Scanning**: Content scanning mitigates structured injection patterns; it does not guarantee detection of novel semantic jailbreaks. Human review remains authoritative.

- **Model Data Placement**: The default model path uses local Ollama. Explicit `YUMMI_EINO_MODEL_BASE_URL` overrides move conductor and agent inference to that compatible endpoint (for both graph and Eino); optional `brain/` has its own provider configuration. Remote inference sends configured context to that provider. Gateway sandbox rules do not constrain orchestrator-originated model traffic.
- **Approval Modes**: `ask_permission` gates each command unless the per-task low/medium-risk override applies. `auto` skips ordinary command gates even for high/critical risk; detected destructive patterns force a gate in both modes. Single-command proposals have their separate risk-based approval path.
