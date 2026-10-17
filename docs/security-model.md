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
| **`block_all`** (Default) | `block_all`, `block` | Denies gateway-forwarded sandbox egress, including DNS and established connections, ignoring active allow rules. Fail-closed. | Routed egress lockdown; local tooling and private policy reads remain available. |
| **`block_all_except_rules`** | `block_all_except_rules`, `rules_only` | Denies outbound traffic by default, but permits destinations explicitly matched by configured scope or active temporary allow rules. | Strict assessment scopes with explicit CIDR and host authorizations. |
| **`allow_all`** | `allow_all`, `allow` | Permits outbound traffic to all public destinations not explicitly blocked. | Broad reconnaissance and public targets without pre-resolving individual IPs. |
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

Because packet filters operate on IP headers, domain-level restrictions (`scope.hostname_allow`, `scope.hostname_block`) are enforced by the orchestrator prior to DNS resolution:

```text
Decision Precedence:
1. Glob match in hostname_block  -> BLOCK (Denies connection; overrides Allow egress mode)
2. Non-empty hostname_allow      -> Must match glob (Whitelist), else BLOCK
3. Default                       -> NEUTRAL (Defers decision to IP egress mode)
```

Wildcards (`*.example.com`) cover both apex domains and arbitrary subdomains. Evaluated domain blocks are recorded in the tamper-evident audit log.

### In-Sandbox Browser & WebSocket Proxy <a id="browser-proxy"></a>

While external packet filtering is enforced at the network perimeter by the gateway firewall, headless browser instances (`rod` / Chromium) execute complex multi-tab navigation, redirections, and WebSockets inside the sandbox container. To prevent unauthorized in-page redirects, iframe escapes, or direct WebSocket bypasses:
- Sandboxed Chromium runs with `--proxy-server=http://127.0.0.1:18080`.
- The in-sandbox proxy (`browser_proxy.go`) intercepts all outbound HTTP/HTTPS `CONNECT` tunnels and plain HTTP requests.
- Before opening an upstream connection, the proxy consults `pc.CheckHostname(targetHost)` against the gateway hostname policy.
- WebSocket handshakes (`Upgrade: websocket`) undergo explicit hostname verification before connection establishment.
- If a target is blocked or out-of-scope, the proxy immediately terminates the request with `HTTP 403 Forbidden` and audits the attempt.

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
6. **Untrusted Web Research Containment**: Data retrieved via `web_search` and `web_fetch` is parsed, stripped of active scripts/styles, bounded in size, and tagged with `UNTRUSTED_WEB_CONTENT` headers before being supplied to the conductor. It is treated as untrusted observational input and is prohibited from automatic permanent memory ingestion.

### Untrusted Web Research Containment <a id="web-research-containment"></a>

External web search results and fetched HTML pages represent uncontrolled, third-party content subject to prompt injection, malicious instructions, or data exfiltration attempts. Yummi protects the model and system through:
- **Strict Sandbox Localization**: `web_search` and `web_fetch` execute solely within the sandbox container under the gateway egress policy. The host orchestrator never initiates direct web queries.
- **Structural Text Transformation**: Raw HTML is converted to clean text (removing `<script>`, `<style>`, inline iframes, and hidden CSS tags) and truncated to safe byte bounds.
- **Untrusted Content Envelope**: Ingested snippets are enclosed in an explicit `UNTRUSTED_WEB_CONTENT / PROMPT_INJECTION_RISK` wrapper so the model evaluates facts without adopting injected persona instructions.
- **No Automatic Memory Insertion**: External web content is never written directly to long-term memory (`REMEMBER` / SQLite knowledge base).
- **Operator Toggle Control**: Web search capability is gated by an operator-controlled switch in the UI (`GET/POST /api/v1/system/web-search`); when toggled off, search tools are disabled and rejected.

### Single-Command Result Review <a id="threatscan-single-command"></a>

When single-command execution output triggers a threat pattern, status moves to `awaiting_threat_approval`. The operator inspects the raw output and explicitly chooses whether to release sanitized results into the conductor context or suppress model feedback while retaining the audit record.

---

## Layer 3: Tamper-Evident Audit Logging <a id="layer-audit"></a>

All scope queries, rule modifications, and egress policy changes are logged to SQLite in WAL mode (`gateway/src/audit/`).

Each log record includes a cryptographic SHA-256 hash that incorporates the hash of the preceding entry:

$$\text{Hash}_n = \text{SHA256}(\text{Record}_n \mathbin{\Vert} \text{Hash}_{n-1})$$

Any modification, deletion, or insertion of historical log entries invalidates the forward hash chain, providing verifiable audit integrity.

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
- Linux capability bounds (`NET_RAW` for raw packet tools, minimal file capabilities).

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
