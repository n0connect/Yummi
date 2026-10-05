[Back to Project README](../README.md) · [Documentation Index](README.md)

# System Architecture

Yummi operates across **five runtime components** organized within **three build ecosystems** (Go, Rust, and Node/React), with an optional Python LangGraph multi-agent runtime. This document details component responsibilities, process isolation, network boundaries, execution pipelines, and data flows.

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
|   | - Fail-Closed DROP Policy     |   |  |   | - 54 Native Pentest MCP Tools  |   |
|   | - Hostname / IP Scope Engine  |   |  |   | - tmux Terminal Sessions       |   |
|   | - SHA-256 Audit Hash Chain    |   |  |   | - XFCE / noVNC Graphical GUI   |   |
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

Two strictly segregated control channels reach the Kali sandbox:

1. **Execution Channel (Control Plane)**:
   - The Go `orchestrator` issues command executions into the sandbox via `docker exec` (strictly proxied by `docker-proxy`).
   - Every command executes as an unprivileged user (`UID 1000:1000`), with an isolated subshell environment (`/bin/bash -lc`), a 10-minute timeout ceiling, and a 1 MiB output buffer limit.
2. **Network Channel (Data Plane)**:
   - The Rust `gateway` manages Linux kernel `FORWARD` rules in the `nftables` subsystem.
   - The sandbox has zero direct external interfaces; its default gateway routes strictly through the gateway daemon (`172.20.0.2`).
   - If the gateway daemon crashes or shuts down, the kernel `DROP` rules persist, guaranteeing a fail-closed boundary.

| Component | Language / Runtime | Primary Responsibility | Isolation & Enforcement Boundary |
|---|---|---|---|
| **orchestrator** | Go (`CGO_ENABLED=1`) | AI Conductor, Graph / Eino state machine, REST/WebSocket API, execution pipeline, ASM, and reporting engine. | Cannot open raw network sockets to target networks or modify firewall rules. |
| **gateway** | Rust binary (`yummi-gateway`) | Installs fail-closed `nftables` FORWARD rules, evaluates egress scope, logs tamper-evident audit trail. | Isolated from LLM logic and direct command execution; manages network filter only. |
| **sandbox** | Kali Linux container (`yummi-pwnbox`) | Unified offensive operations workbench (54 MCP & terminal tools, Metasploit, Nishang, Kerbrute, OpenVPN), operator Bash (`tmux`), and graphical Desktop (`noVNC`/XFCE). | No default internet route; egress is restricted to the gateway proxy container. |
| **docker-proxy** | `tecnativa/docker-socket-proxy` | Filters Docker API requests from orchestrator to host daemon. | Restricts API access to container exec and inspect; container lifecycle mutations are blocked. |
| **ui** | React 18 + TypeScript (nginx) | Web operator interface; proxies `/api` and `/ws` to orchestrator. | Connects strictly to orchestrator; no direct access to gateway or sandbox. |

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

### Key Subsystems:
- **54 Sandbox-Resident MCP Tools (`sandbox-mcp`)**:
  - **6 Typed Tools**: `nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`.
  - **39 Cataloged Pentest Tools**: `httpx_probe`, `dnsx_resolve`, `alterx_mutate`, `gau_fetch`, `katana_crawl`, `dalfox_scan`, `nuclei_scan`, `wpscan_scan`, `wafw00f_scan`, `sqlmap_scan`, `amass_enum`, `subfinder_enum`, `theharvester_scan`, `dnsenum_scan`, `dnsrecon_scan`, `enum4linux_scan`, `onesixtyone_scan`, `snmpwalk_enum`, `dirb_scan`, `hydra_bruteforce`, `medusa_bruteforce`, `checksec_audit`, `radare2_disasm`, `gitleaks_scan`, `masscan_scan`, `sslscan_probe`, `arjun_scan`, `naabu_scan`, `tlsx_scan`, `cloud_enum_scan`, `jwt_tool_inspect`, `subzy_scan`, `jsluice_scan`, `graphql_cop_scan`, `corsy_scan`, `screenshot_triage`, `feroxbuster_scan`, `commix_scan`, `whois_query`.
  - **2 Concurrency Utilities**: `consolidate_hosts`, `passive_recon_batch`.
  - **7 Persistent Terminal Session Tools**: `term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close` (persistent tmux sessions for background listeners, VPNs, and interactive CLI programs).
- **Active Verification Exploit Oracles**:
  - `OracleActivePoC`: Confirms SQL injection errors, command execution, SSRF metadata responses.
  - `OracleDOMExecution`: Validates XSS and DOM sink execution.
  - `OracleOOBInteraction`: Confirms out-of-band DNS/HTTP callback markers.
  - `OracleDifferential`: Evaluates status code, timing, and response body divergence.
- **Hierarchical Asset Tree Store**: SQLite-persisted graph indexing domains, subdomains, IPs, ports, endpoints, parameters, and metadata.
- **Program Scope Rules & Report Gate**: Session-level scope definitions (`program_scope_rules`) with wildcard matching, real-time hostname classification, and automatic filtering of out-of-scope assets from final reports.
- **Multi-Format Report Generator**: Produces HackerOne Markdown, Bugcrowd VRT, Executive Management summaries, and JSON.
- **Two-Stage Exploitation Lab**: Built-in testbed container (`lab-target`, `198.51.100.10`) with Apache path-traversal RCE (CVE-2021-41773) and local SUID python privesc.
- **Proxy & Traffic Ingestion Bridge**: Ingests HTTP flows from Burp Suite, Caido, and Mitmproxy into the Knowledge Graph.

---

## Process Isolation & Security Boundaries <a id="why-separate-processes"></a>

```text
+-----------------------------------------------------------------------------------+
|                               ISOLATION GUARANTEES                                |
|                                                                                   |
|   1. LLM Context Compromise -> Isolated in Orchestrator RAM (Cannot change rules) |
|   2. Sandbox Escape / Tool Exploit -> Contained in Container (Non-root UID 1000)  |
|   3. Network Scope Enforcement -> Kernel-Level nftables (Rust Gateway)            |
|   4. Gateway Daemon Termination -> Kernel DROP-All Rules Persist (Fail-Closed)   |
|   5. Audit Integrity -> SHA-256 Hash Chain per Decision (Tamper-Evident)          |
+-----------------------------------------------------------------------------------+
```

---

## Network Topology & Routing <a id="network-topology"></a>

```text
yummi_internal (172.20.0.0/24, internal: true, no default Docker gateway)
  |-- gateway         172.20.0.2   (Default gateway & router for sandbox)
  |-- orchestrator    172.20.0.3
  |-- ui              172.20.0.4
  |-- docker-proxy    172.20.0.5
  \-- sandbox         172.20.0.10  (Only attached to yummi_internal)

yummi_external (Bridge driver with external network connectivity)
  |-- gateway         (NAT router subject to nftables FORWARD rules)
  |-- orchestrator    (Reaches LLM API endpoints)
  \-- ui              (Exposed on host port 3000)
```

---

## Data Flows <a id="data-flows"></a>

### Single Command Execution <a id="flow-single-command"></a>

```text
Operator / UI                Orchestrator                    Gateway Daemon             Sandbox Container
      |                            |                               |                            |
      |-- 1. POST /chat ---------->|                               |                            |
      |   "scan 10.0.0.5"          |-- 2. Conductor Proposes Cmd ->|                            |
      |<-- 3. Proposal Prompt -----|                               |                            |
      |                            |                               |                            |
      |-- 4. POST /chat/approve -->|                               |                            |
      |                            |-- 5. CheckHostname / Scope -->|                            |
      |                            |<-- 6. Verdict: "allow" -------|                            |
      |                            |-- 7. AddTemporaryRule ------->| (Opens nftables rule)      |
      |                            |                               |                            |
      |                            |-- 8. docker exec (proxied) ------------------------------->|
      |                            |      "nmap -sV 10.0.0.5"      |                            | (Executes command)
      |                            |<-- 9. Command Output (Stdout/Stderr) ----------------------|
      |<-- 10. WS /ws/events ------|                               |                            |
```

### Autonomous Agent Task Execution <a id="flow-agentic-task"></a>

```text
State: [step] ---> [tool] ---> [approval] (if risk >= medium) ---> [verify] ---> [step]
          |           |                                               |
          |           +-----> [handoff] (concurrent sub-agents)       |
          |           |                                               v
          |           +-----> [threat_hold] (if output flagged)    [finish]
          v
       [ask] (solicits operator input)
```

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
| **Python Package** | `yummi-brain` | `brain/` | `src/yummi_brain/cli.py` |

---

## Non-Goals and Out-of-Scope Boundaries <a id="not-in-scope"></a>

- **Model Weight Bundling**: Yummi ships zero model weights. Local inference is provided by an external Ollama daemon; cloud inference uses external API endpoints.
- **Dormant Host MCP Client**: `internal/mcp` contains a standalone stdio MCP client that is currently dormant and not integrated into the production agent loop.
- **Cross-Platform Host Network Control**: Gateway network enforcement is strictly Linux `nftables`-based. On macOS and Windows, gateway runs within a Linux VM provided by the container runtime.
