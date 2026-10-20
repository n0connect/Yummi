[Back to Project README](../README.md) · [Documentation Index](README.md)

# System Architecture

Yummi operates across **five runtime components** organized within **Go, Rust, and Node/React build ecosystems**, with an optional Python LangGraph runtime. This document details component responsibilities, process isolation, network boundaries, execution pipelines, and data flows.

Core Invariant: **The LLM proposes actions; deterministic software controls approval, target validation, command execution, and network policy.**

---

## Table of Contents

- [Component Topology](#the-five-components)
- [Control Plane & Execution Channels](#control-channels)
- [Attack Surface Management & Web Pentest Engine](#redteam-architecture)
- [Process Isolation & Security Boundaries](#why-separate-processes)
- [Network Topology & Routing](#network-topology)
- [Data Flows](#data-flows)
  - [Single Command Execution](#flow-single-command)
  - [Autonomous Agent Task Execution](#flow-agentic-task)
  - [Sub-Agent Delegation Fan-Out](#flow-delegation)
  - [Verification Oracle Pipeline](#flow-oracle)
  - [Proxy & Traffic Ingestion Bridge](#flow-proxy)
- [Ecosystem Organization & Modules](#build-ecosystems)
- [Non-Goals and Out-of-Scope Boundaries](#not-in-scope)

---

## Component Topology <a id="the-five-components"></a>

```text
+-----------------------------------------------------------------------------------+
|                                 OPERATOR WORKSPACE                                |
|                                                                                   |
|   +---------------------------------------------------------------------------+   |
|   |                        ui (React 18 + TypeScript SPA)                     |   |
|   |   Agent | Terminals | Trace | Bash | Desktop | Logs | Gateway | Health    |   |
|   +-------------------------------------+-------------------------------------+   |
|                                         | HTTP / REST (:3000 -> :9080)            |
|                                         | WebSocket (/ws/events, /ws/terminal)    |
+-----------------------------------------|-----------------------------------------+
                                          v
+-----------------------------------------------------------------------------------+
|                               ORCHESTRATOR SUBSTRATE                              |
|                                                                                   |
|   +---------------------------------------------------------------------------+   |
|   |                     orchestrator (Go Service :9080)                       |   |
|   |                                                                           |   |
|   |   +-------------------+  +--------------------+  +--------------------+   |   |
|   |   | Conductor Engine  |  | Graph / Eino Agent |  | Knowledge Store    |   |   |
|   |   | (Chat & Personas) |  | (State Machine)    |  | (Memory, ASM, PoC) |   |   |
|   |   +---------+---------+  +---------+----------+  +---------+----------+   |   |
|   |             |                      |                       |              |   |
|   |             +----------------------+-----------------------+              |   |
|   |                                    v                                      |   |
|   |                     executor.RunScopedCommand                             |   |
|   |                     (Target Extraction & Verification)                    |   |
|   +--------------------+--------------------------------+---------------------+   |
|                        |                                |                         |
+------------------------|--------------------------------|-------------------------+
                         | Unix IPC Socket                | docker exec (proxied)
                         v                                v
+---------------------------------------+  +----------------------------------------+
|           GATEWAY DAEMON              |  |            SANDBOX CONTAINER           |
|                                       |  |                                        |
|   +-------------------------------+   |  |   +--------------------------------+   |
|   | yummi-gateway (Rust)          |   |  |   | sandbox (Hardened Kali Linux)  |   |
|   |                               |   |  |   | UID 1000 (pentester user)      |   |
|   | - nftables FORWARD Rules      |   |  |   |                                |   |
|   | - Fail-Closed DROP Policy     |   |  |   | - Sandbox MCP & Pentest Tools |   |
|   | - Hostname / IP Scope Engine  |   |  |   | - tmux Terminal Sessions       |   |
|   | - SHA-256 Audit Hash Chain    |   |  |   | - Browser & Desktop (noVNC)    |   |
|   +---------------+---------------+   |  |   +----------------+---------------+   |
|                   |                   |  |                    |                   |
+-------------------|-------------------+  +--------------------|-------------------+
                    |                                           |
                    |  nftables FORWARD (Allow / Deny)          | Egress packets
                    +===========================================+
                                          |
                                          v (Target External Network)
```

---

## Control Plane & Execution Channels <a id="control-channels"></a>

Two separate channels control execution and routed traffic:

1. **Execution Channel (Control Plane)**:
   - The Go `orchestrator` issues command executions via the constrained `docker-proxy` broker on internal control `172.21.0.0/24`. The broker accepts the exact orchestrator peer, the configured sandbox container and UID/GID `1000:1000`; it tracks issued exec IDs and rejects lifecycle/other-container operations. Sandbox and gateway do not join this network.
   - One-shot shell commands execute as user (`UID 1000:1000`), in a fresh shell sharing the container filesystem and process namespace (`/bin/bash -lc`), a configurable timeout (10 minutes by default), and separate 1 MiB stdout/stderr capture limits. Persistent MCP terminal sessions have their own lifecycle and are not limited to that one-shot shell timeout.
2. **Network Channel (Data Plane)**:
   - The Rust `gateway` manages Linux kernel `FORWARD` rules in the `nftables` subsystem.
   - The sandbox has zero direct external interfaces; its default gateway routes strictly through the gateway daemon (`172.20.0.2`).
   - If the gateway daemon crashes or shuts down, the last committed kernel rules persist while its network namespace survives. Existing permissions, including `allow_all`, can therefore remain; a restart installs `block_all` before reopening IPC.

| Component | Language / Runtime | Primary Responsibility | Isolation & Enforcement Boundary |
|---|---|---|---|
| **orchestrator** | Go (`CGO_ENABLED=1`) | AI Conductor, Graph state machine / Eino ReAct loop, REST/WebSocket API, execution pipeline, ASM, and reporting engine. | Drops all container capabilities and cannot modify gateway firewall rules directly. It joins the external bridge for model connectivity; the sandbox firewall does not constrain orchestrator-originated traffic. |
| **gateway** | Rust binary (`yummi-gateway`) | Installs fail-closed `nftables` FORWARD rules, evaluates egress scope, logs tamper-evident audit trail. | Isolated from LLM logic and direct command execution; manages network filter only. |
| **sandbox** | Kali Linux container (`yummi-pwnbox`) | Unified offensive operations workbench (MCP, browser, and terminal tools, Metasploit, Nishang, Kerbrute, OpenVPN), operator Bash (`tmux`), and graphical Desktop (`noVNC`/XFCE). | Default route points to gateway; off-subnet traffic crosses its FORWARD policy. Local/same-subnet traffic does not. |
| **docker-proxy** | Go broker (`orchestrator/cmd/docker-proxy`) | Filters Docker API requests from orchestrator to host daemon. | Restricts API access to container exec and inspect; container lifecycle mutations are blocked. |
| **ui** | React 18 + TypeScript (nginx) | Web operator interface; proxies `/api` and `/ws` to orchestrator. | Joins control and UI-only ingress bridges; proxies operator requests to orchestrator. It does not join the sandbox network. |

---

## Attack Surface Management & Web Pentest Engine <a id="redteam-architecture"></a>

Yummi includes a dedicated offensive security and bug bounty substrate:

```text
+-----------------------------------------------------------------------------------+
|                        ATTACK SURFACE & RECONNAISSANCE MATRIX                     |
|                                                                                   |
|   Root Target (e.g., example.com)                                                 |
|     |                                                                             |
|     +---> Subdomains (api.example.com, auth.example.com, vault.example.com)      |
|     |       |                                                                     |
|     |       +---> IP Addresses & Open Ports (10.0.0.5:80, 10.0.0.5:443)           |
|     |               |                                                             |
|     |               +---> HTTP Endpoints (GET /v1/users, POST /v1/auth/login)     |
|     |                       |                                                     |
|     |                       +---> Parameters (?id=1001, ?debug=true, ?token=...)  |
|     |                               |                                             |
|     |                               +---> Discovered Secrets & Cloud Storage      |
+-----------------------------------------------------------------------------------+
```

### Key Subsystems

- **Sandbox-resident MCP tools (`sandbox-mcp`)**: typed pentest wrappers, cataloged tools, recon utilities, persistent tmux terminals, Chromium automation, and sandbox-only `web_search` / `web_fetch`. Discover actual schemas through MCP `tools/list`; see [the tool reference](agent/sandbox-execution.md).
- **In-sandbox browser connection proxy (`sandbox-mcp/browser_proxy.go`)**: Chromium uses an ephemeral loopback HTTP proxy. It checks non-loopback destination hostname policy before DNS/dial, supports CONNECT and WebSocket upgrades, and leaves IP packet enforcement to gateway. This guard applies to managed Chromium, not every arbitrary shell tool.
- **Verification helpers (`orchestrator/internal/agent/oracle.go`)**: classify supplied output/evidence using response patterns, canary-token matches, HTTP status transitions, and relative response-length differences. The `OracleDOMExecution` label does not itself prove JavaScript ran in a browser, and `OracleOOBInteraction` does not run an independent callback collector. Treat these as evidence heuristics that need review, rather than conclusive exploit verification.
- **Hierarchical Asset Tree Store**: SQLite-persisted graph indexing domains, subdomains, IPs, ports, endpoints, parameters, and metadata.
- **Program Scope Rules & Report Gate**: Session-level scope definitions (`program_scope_rules`) classify assets for reporting. They are distinct from gateway packet scope and do not themselves grant or revoke network access.
- **Multi-Format Report Generator**: Produces HackerOne Markdown, Bugcrowd VRT, Executive Management summaries, and JSON.
- **Two-Stage Exploitation Lab**: Built-in testbed container (`lab-target`, `198.51.100.10`) with Apache path-traversal RCE (CVE-2021-41773) and local SUID python privesc.
- **Proxy & Traffic Ingestion Bridge**: Accepts normalized flow JSON to create knowledge assets; integrations must supply that JSON explicitly.

---

## Process Isolation & Security Boundaries <a id="why-separate-processes"></a>

- Model actions pass through the execution store, approval controls, and gateway checks.
- Tool commands run as UID 1000 in the shared Kali container; file capabilities, root compromise, and escape remain relevant limits.
- Gateway enforces source-IP-based packet policy on forwarded traffic; it cannot filter same-subnet traffic it never sees.
- Gateway termination retains the last committed kernel rules while its namespace survives.
- Audit hashes provide tamper evidence for recorded fields/events, not a complete packet log or an authenticated external ledger.

---

## Network Topology & Routing <a id="network-topology"></a>

```text
yummi_internal (172.20.0.0/24, internal: true)
  |-- gateway         172.20.0.2   (Sandbox's routed egress)
  |-- orchestrator    172.20.0.3   (Read-only policy listener :9081)
  \-- sandbox         172.20.0.10  (Only attached to this network)

yummi_control (172.21.0.0/24, internal: true)
  |-- orchestrator    172.21.0.3   (Main API :9080, exact peer authorization)
  |-- ui              172.21.0.4   (Trusted operator proxy)
  \-- docker-proxy    172.21.0.5   (Constrained Docker broker)

yummi_ingress (172.22.0.0/24, normal bridge)
  \-- ui              172.22.0.4   (Only UI joins; bridge gateway .1)

yummi_external (Normal bridge): gateway and orchestrator only
Host 127.0.0.1:3000 and :9080 -> UI nginx :80 -> orchestrator :9080
No orchestrator host port is published.
```

The main API binds `0.0.0.0:9080` inside the container. Peer authorization
restricts operator requests to loopback and UI `172.21.0.4/32`, using the
socket address rather than caller-supplied forwarded headers. The private
`:9081` router exposes hostname policy and literal-IP egress decisions only;
it cannot approve commands, change modes or add rules.

nginx additionally checks local Host names and exact configured source peers.
In isolated OrbStack published-port checks, the host arrived as
`172.22.0.1`; the allow rule covers that exact address. The control bridge stays
internal; the separate UI-only normal ingress bridge makes loopback publishing
work without placing sandbox or broker on ingress.

`block_all` denies gateway-forwarded sandbox egress, including DNS and established
connections. The other modes retain an explicit gateway-routed UDP/53 resolution
exception after destination blocks. Every forwarded sandbox packet rechecks current
scope. In rules-only/ask mode, expiry/removal also blocks established flows that
depended on that approval and have no other matching allow. `allow_all` does not
require temporary approvals. Local and same-subnet traffic, including read-only
policy requests, does not traverse the gateway FORWARD chain.

This describes the checked-in Compose configuration. Source inspection does not
establish the network membership, image version, or egress mode of a running deployment.

---

## Data Flows <a id="data-flows"></a>

### Single Command Execution <a id="flow-single-command"></a>

```text
Operator / UI                Orchestrator                    Gateway Daemon             Sandbox Container
      |                            |                               |                            |
      |-- 1. POST /chat ---------->|                               |                            |
      |   "scan 10.0.0.5"          |   2. Conductor proposes locally |                            |
      |<-- 3. Proposal Prompt -----|                               |                            |
      |                            |                               |                            |
      |-- 4. POST /chat/approve -->|                               |                            |
      |                            |-- 5. CheckHostname / Scope -->|                            |
      |                            |<-- 6. Verdict: "allow" -------|                            |
      |                            |-- 7. AddTemporaryRule* ------->| (Opens nftables rule)      |
      |                            |                               |                            |
      |                            |-- 8. docker exec (proxied) ------------------------------->|
      |                            |      "nmap -sV 10.0.0.5"      |                            | (Executes command)
      |                            |<-- 9. Command Output (Stdout/Stderr) ----------------------|
      |<-- 10. WS /ws/events ------|                               |                            |
```

*Temporary rules are opened as needed by the executor; `allow_all` needs no temporary authorization. A blocked verdict fails execution; `needs_approval` holds the record for operator egress approval, then requeues it after authorization.

### Autonomous Agent Task Execution <a id="flow-agentic-task"></a>

```text
step -> tool -> approval (when required) -> execute/verify -> step
  |       |                 |
  |       +-> consult       +-> threat_hold (flagged output)
  +-> handoff / remember / skill_view / summarize / retry
  +-> finish / ask (terminal nodes)
```

`verify` is processing performed during tool/result handling, not a graph node.
In `ask_permission` mode every command gates unless the per-task
`auto_approve_non_critical` override permits a low/medium-risk command. `auto`
skips normal command gates; destructive-command floors still require approval.
Gateway egress approvals are independent. Graph MCP arguments are frozen before
approval; callers must approve the exact proposal and structured arguments.

### Verification Oracle Pipeline <a id="flow-oracle"></a>

Captured output and model-supplied finding metadata feed the verification helpers.
The helpers match evidence against output and classify the proof kind; see the
limitations above and [verification](agent/execution-provenance.md). An evidence label or
confidence score is not a substitute for reproducing the finding.

### Proxy & Traffic Ingestion Bridge <a id="flow-proxy"></a>

The operator may submit normalized flow JSON through `POST /api/v1/proxy/inject`
or the TRACE UI. The bridge creates host/endpoint/query-parameter assets from
the supplied URL/method/status; request and response bodies are not persisted
by this handler. Status fields such as `ca_installed` are static metadata, not
a live certificate installation check; the bridge does not automatically attach to a running Burp/Caido instance.
See [proxy bridge](api-reference.md).

### Sub-Agent Delegation Fan-Out <a id="flow-delegation"></a>

```text
Parent Agent Task (Top-Level)
      |
      +---> [HANDOFF] {"handoffs": [{"role": "api-hunter", ...}, {"role": "js-miner", ...}]}
              |
              +---> Child 1: api-hunter (Graph State Machine, Scoped to API skills)
              |       |---> Records findings -> Shares Knowledge Store
              |       +---> Emits brief summary -> Merged into Parent Observation
              |
              +---> Child 2: js-miner (Graph State Machine, Scoped to JS mining)
                      |---> Extracts endpoints -> Updates ASM Asset Tree
                      +---> Emits brief summary -> Merged into Parent Observation
```

---

## Ecosystem Organization & Modules <a id="build-ecosystems"></a>

| Ecosystem | Component | Path | Entry Point |
|---|---|---|---|
| **Go Module (CLI)** | `yummi` | `./` | `cmd/yummi/main.go` |
| **Go Module (Orchestrator)** | `yummi-orchestrator` | `orchestrator/` | `cmd/orchestrator/main.go` |
| **Go Module (Sandbox MCP)** | `yummi-sandbox-mcp` | `sandbox-mcp/` | `main.go` |
| **Rust Crate** | `yummi-gateway` | `gateway/` | `src/main.rs` |
| **Node / React** | `ui` | `ui/` | `src/main.tsx` |
| **Python Package** | `yummi-brain` | `brain/` | `src/yummi_brain/main.py` |

---

## Non-Goals and Out-of-Scope Boundaries <a id="not-in-scope"></a>

- **Model Weight Bundling**: Yummi ships zero model weights. Local inference is provided by an external Ollama daemon; cloud inference uses external API endpoints.
- **Dormant Host MCP Client**: `internal/mcp` contains a standalone stdio MCP client that is currently dormant and not integrated into the production agent loop.
- **Cross-Platform Host Network Control**: Gateway network enforcement is strictly Linux `nftables`-based. On macOS and Windows, gateway runs within a Linux VM provided by the container runtime.
