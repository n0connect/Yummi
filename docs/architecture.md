[Back to Project README](../README.md) · [Documentation Index](README.md)

# System Architecture

Yummi operates across **five runtime components** organized within **three build ecosystems** (Go, Rust, and Node/React). This document details the component responsibilities, process isolation model, network boundaries, and execution pathways.

Core Invariant: **The LLM proposes actions; deterministic software controls approval, target validation, command execution, and network policy.**

## Table of Contents

- [Component Topology](#the-five-components)
- [LangGraph Brain Integration](#brain)
- [Process Isolation Model](#why-separate-processes)
- [Network Segmentation](#network-topology)
- [Data Flows](#data-flows)
  - [Single Command Execution](#flow-single-command)
  - [Autonomous Agent Task](#flow-agentic-task)
  - [Approval Gate Lifecycle](#flow-approval-gate)
- [Ecosystem Organization](#build-ecosystems)
- [Non-Goals and Out-of-Scope Boundaries](#not-in-scope)

---

## Component Topology <a id="five-components"></a>

```text
                        +----------------------+
       Operator ------->|   ui (React/nginx)   |  :3000
                        +----------+-----------+
                                   | /api, /ws (proxied)
                        +----------v-----------+
       CLI ------------>|  orchestrator (Go)   |  :9080 (127.0.0.1 only)
                        |  conductor + agent   |
                        +----+------------+----+
                             |            |
                 IPC Socket  |            | Docker Exec (via docker-proxy)
        (Length-prefixed)    |            |
                        +----v-------+    |
                        | gateway    |    |
                        | (Rust)     |    |
                        | nftables   |    |
                        +----+-------+    |
                             |            |
               FORWARD Chain |            |
                (Allow/Deny) |            |
                        +----v------------v----+
                        |    sandbox (Kali)    |  No direct internet route;
                        |                      |  egress gated by gateway's
                        |                      |  FORWARD chain.
                        +----------------------+
```

Two distinct control channels reach the sandbox container:
1. **Execution Channel**: Orchestrator issues commands into the sandbox via `docker exec` (proxied and constrained by `docker-proxy`).
2. **Network Channel**: Gateway manages kernel `FORWARD` rules in the network namespace, determining which external destinations the sandbox may reach.

| Component | Language / Runtime | Primary Responsibility | Isolation & Enforcement Boundary |
|---|---|---|---|
| **orchestrator** | Go binary (`CGO_ENABLED=1`) | AI conductor, agent graph/Eino state machine, REST/WebSocket API, execution pipeline. | Cannot open raw network sockets to target networks or modify firewall rules. |
| **gateway** | Rust binary (`yummi-gateway`) | Installs fail-closed `nftables` FORWARD rules, evaluates egress scope, logs tamper-evident audit trail. | Isolated from LLM logic and direct command execution; manages network filter only. |
| **sandbox** | Kali Linux container | Unified environment for tool execution, operator Bash (`tmux`), and graphical Desktop (`noVNC`/XFCE). | No default internet route; egress is restricted to the gateway proxy container. |
| **docker-proxy** | `tecnativa/docker-socket-proxy` | Filters Docker API requests from orchestrator to host daemon. | Restricts API access to container exec and inspect; lifecycle mutations are blocked. |
| **ui** | React 18 + TypeScript (nginx) | Single-page operator interface; proxies `/api` and `/ws` to orchestrator. | Connects strictly to orchestrator; no direct access to gateway or sandbox. |

The web interface is the exclusive operator dashboard. The root `yummi` CLI binary manages local Docker service lifecycles and launches the browser interface.

### Docker Socket Proxy (`docker-proxy`) <a id="docker-proxy"></a>

To prevent exposing the host `/var/run/docker.sock` directly to the Go orchestrator, `docker-proxy` mediates all container API communication:

```yaml
CONTAINERS: 1       # Read-only container inspection (GET /containers/*)
EXEC: 1             # Execution attachment (POST /containers/{id}/exec)
POST: 1             # Required for exec instance creation
# All other API resource families explicitly set to 0:
# ALLOW_START, ALLOW_STOP, ALLOW_RESTARTS, BUILD, COMMIT, IMAGES, NETWORKS, VOLUMES...
```

### LangGraph Agent Brain (`brain/`) <a id="brain"></a>

The `brain/` directory houses an optional Python runtime using LangGraph. It provides a multi-agent supervisor/specialist architecture powered by frontier cloud models (via OpenRouter or OpenAI-compatible endpoints). 

From a security architecture perspective, `brain/` acts as an external REST and MCP client to the orchestrator. Every command dispatched by `brain/` flows through the standard `executor.RunScopedCommand` pipeline and remains subject to the gateway's fail-closed network policies.

### Web Pentest and Bug Bounty Architecture <a id="redteam-architecture"></a>

Yummi is built specifically for autonomous offensive security workflows:
- **Target-First Launcher**: Operators submit a target domain, URL, or CIDR to trigger automated reconnaissance and assessment playbooks.
- **30 Sandbox-Resident MCP Tools (`sandbox-mcp`)**: 6 typed tools (`nmap`, `whatweb`, `curl`, `gobuster`, `ffuf`, `nikto`), 22 cataloged tools (`httpx`, `dnsx`, `alterx`, `gau`, `katana`, `dalfox`, `nuclei`, `wpscan`, `wafw00f`, `sqlmap`, `amass`, `subfinder`, `theharvester`, `dnsenum`, `enum4linux`, `onesixtyone`, `snmpwalk`, `dirb`, `hydra`, `medusa`, `checksec`, `radare2`), and 2 concurrency utilities (`consolidate_hosts`, `passive_recon_batch`).
- **Artifact Pipeline**: Findings from discovery tools are structured under `/workspace/` and fed forward into subsequent assessment stages.
- **Integrated Desktop GUI**: The sandbox environment includes Burp Suite, Wireshark, and Firefox accessible directly via the noVNC/XFCE desktop tab.

---

## Process Isolation Model <a id="why-separate-processes"></a>

Firewall management and AI reasoning are deliberately split across distinct OS processes and programming languages:

1. **Failure Containment**: A prompt injection vulnerability, malformed parser input, or unexpected LLM output cannot alter kernel packet rules.
2. **Fail-Closed Guarantee**: The Rust gateway daemon establishes DROP-all firewall rules at startup before binding its IPC socket. If the AI orchestrator or the gateway daemon crashes, the kernel-level DROP rules remain active in the network namespace.
3. **Memory Safety and Concurrency**: Rust's compile-time borrow checker and absence of garbage collection jitter ensure reliable, deterministic rule evaluation.

---

## Network Segmentation <a id="network-topology"></a>

```text
yummi_internal (172.20.0.0/24, internal: true, no default Docker gateway)
  ├── gateway         172.20.0.2   (Default router for sandbox)
  ├── orchestrator    172.20.0.3
  ├── ui              172.20.0.4
  ├── docker-proxy    172.20.0.5
  └── sandbox         172.20.0.10  (Connected only to yummi_internal)

yummi_external (Bridge driver with external network connectivity)
  ├── gateway         (Performs NAT/routing subject to nftables policy)
  ├── orchestrator    (Reaches LLM endpoints / APIs)
  └── ui
```

Key Architectural Properties:
- **Isolated Sandbox**: `sandbox` has no interface on `yummi_external`. Its default routing table directs all external traffic through `gateway` (`172.20.0.2`).
- **Deterministic DNS**: Both `sandbox` and `orchestrator` resolve hostnames against Cloudflare DNS (`1.1.1.1`, `1.0.0.1`). DNS queries pass through the gateway on UDP port 53, while all TCP/UDP connections to resolved IPs require explicit gateway allow rules.

---

## Data Flows <a id="data-flows"></a>

### Single Command Execution <a id="flow-single-command"></a>

```text
1. UI -> POST /api/v1/chat {"message": "scan target 10.0.0.5"}
2. Orchestrator conductor evaluates intent and drafts command proposal.
3. Operator reviews proposal and submits approval: POST /api/v1/chat/approve
4. execution.Store enqueues command -> executor.ProcessFunc picks up execution.
5. executor.RunScopedCommand:
   a. Extracts target IPs and hostnames.
   b. Evaluates name-level policy via gateway.CheckHostname.
   c. Queries gateway.CheckEgress for target IP addresses.
   d. Adds temporary scoped nftables allow rule via gateway.AddTemporaryRule.
   e. Dispatches execution to sandbox: docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"
6. Command output is returned and broadcast via WebSocket /ws/events.
```

### Autonomous Agent Task <a id="flow-agentic-task"></a>

```text
1. UI -> POST /api/v1/chat (Conductor identifies goal requiring a task plan)
2. agent.Runner.CreateAndPlan generates structured numbered plan steps.
3. agent.Runner.Start initiates the graph execution engine (graph.Execute).
4. State machine evaluates nodes:
   - step: LLM selects next action based on current observations and playbooks.
   - tool: Validates and executes scheduled commands through the scoped executor.
   - handoff: Dispatches sub-tasks to concurrent specialist sub-agents.
   - threat_hold: Pauses execution if tool output triggers threat patterns.
   - finish / ask: Terminates task or solicits operator input.
5. Task status, evidence digests, and structured findings stream live via /ws/events.
```

### Approval Gate Lifecycle <a id="flow-approval-gate"></a>

```text
Command risk classified (medium/high/critical) or mode set to ask_permission:
  -> gate.Request blocks the task's execution goroutine.
  -> Task status transitions to awaiting_approval.
  -> WebSocket broadcasts pending_command and pending_risk metadata to UI.
  -> Operator resolves via POST /api/v1/tasks/{id}/approve or /api/v1/chat/approve.
  -> gate.Resolve unblocks execution channel; command proceeds to scoped executor.
```

---

## Ecosystem Organization <a id="build-ecosystems"></a>

| Ecosystem | Component | Path | Entry Point |
|---|---|---|---|
| **Go Module (CLI)** | `yummi` | `./` | `cmd/yummi/main.go` |
| **Go Module (Orchestrator)** | `yummi-orchestrator` | `orchestrator/` | `cmd/orchestrator/main.go` |
| **Go Module (Sandbox MCP)** | `yummi-sandbox-mcp` | `sandbox-mcp/` | `main.go` |
| **Rust Crate** | `yummi-gateway` | `gateway/` | `src/main.rs` |
| **Node / React** | `ui` | `ui/` | `src/main.tsx` |
| **Python Package** | `yummi-brain` | `brain/` | `src/yummi_brain/cli.py` |

The three separate Go modules ensure clean dependency boundaries: the CLI remains lightweight without server dependencies, and `sandbox-mcp` compiles directly into the Kali container without linking against the orchestrator.

---

## Non-Goals and Out-of-Scope Boundaries <a id="not-in-scope"></a>

- **Model Weight Bundling**: Yummi ships zero model weights. Local inference is provided by an external Ollama daemon; cloud inference uses external API endpoints.
- **Dormant Host MCP Client**: `internal/mcp` contains a standalone stdio MCP client that is currently dormant and not integrated into the production agent loop.
- **Cross-Platform Host Network Control**: Gateway network enforcement is strictly Linux `nftables`-based. On macOS and Windows, gateway runs within a Linux VM provided by the container runtime.
