[Back to Project README](../../README.md) · [Docs index](../README.md)

# Module: orchestrator (Go)

This page maps `orchestrator/` packages by **architectural boundary** — what each component owns, and what it deliberately does not touch.

`orchestrator/go.mod` (`github.com/n0connect/yummi/orchestrator`) is an independent Go module, isolated from root CLI and `sandbox-mcp`.

---

## Table of Contents

- [Entry Point](#entry-point)
- [Package Map](#package-map)
- [The Leaf Packages (Zero Upstream Dependencies)](#leaf-packages)
- [The Conductor / Executor Boundary Rule](#where-things-plug-in)

---

## Entry Point <a id="entry-point"></a>

`cmd/orchestrator/main.go` wires all packages into the Chi router (`internal/api/routes.go`) and starts the HTTP and WebSocket listeners. Dependencies are instantiated explicitly without reflection or magic containers.

---

## Package Map <a id="package-map"></a>

| Package | Responsibility | Deliberately Excluded Concerns |
|---|---|---|
| **`internal/agent`** | Defines `TaskRunner` lifecycle interface, graph state machine (`graph.go`), tag protocol (`protocol.go`), gates (`gate.go`), active verification exploit oracles (`oracle.go`), and delegation (`node_handoff.go`). | Command execution mechanics (`exec.go` strictly enqueues). |
| **`internal/agent/eino`** | Alternate `TaskRunner` implementation using CloudWeGo Eino ADK for native tool-calling on larger models. Wires 54 sandbox tools, 7 persistent terminal session verbs (`terminal_tools.go`), and reuses `Execer`, `Verify`, `Gate`, `threatscan`, and `conductor.Validator`. | Process execution mechanics (uses orchestrator executor). |
| **`internal/api`** | REST routes, WebSocket bridges (`terminal.go`, `desktop.go`), and SSE logs. Handlers organized by domain (`handlers_session.go`, `handlers_assets.go`, `handlers_report.go`, `handlers_proxy.go`, `handlers_gateway.go`, `handlers_system.go`, `handlers_extended.go`). | LLM internals and raw network socket access. |
| **`internal/conductor`** | AI conductor engine (`engine.go`), risk classification floor (`validator.go`), TTL proposal store (`approval.go`), model tiers (`tier.go`), and persona fixtures (`golden.go`). | Network firewall rules and command execution. |
| **`internal/knowledge`** | World model: targets, security findings, procedural tactics, persistent context, Attack Surface Management (`assets.go`), and program scope rules (`program_scope.go`). | Firewall manipulation and execution dispatch. |
| **`internal/report`** | Multi-Format Reporting Engine: compiles session data into HackerOne Markdown, Bugcrowd VRT Markdown, Executive Management summaries, and JSON with out-of-scope filtering. | Session persistence and network execution. |
| **`internal/skill`** | Declarative YAML offensive playbooks (22 skills); content threat-scanned on load. | Direct binary execution. |
| **`internal/skillcurator`** | Autonomous skill creation: sanitizes successful trajectories (`sanitize.go`) and quarantines drafts for operator review. | Live activation without operator consent. |
| **`internal/security/threatscan`** | Content-layer prompt-injection, exfiltration, C2, and unicode scanner. | Network rules and process spawning. |
| **`internal/security/invariants`** | AST-walking fitness function tests locking execution invariants in CI. | Production runtime operations. |
| **`internal/scope`** | Single source of truth for `ExtractTargets(command)` across IPs, CIDRs, and URLs. | Reachability verdicts (sole authority is gateway). |
| **`internal/execution`** | Execution lifecycle store: state transitions (`queued` -> `running` -> `completed` / `failed`), output buffering, rationale tracking (`execution.ContextWithRationale`), and heartbeat ticks. | Policy decisions. |
| **`internal/executor`** | `RunScopedCommand` and `processMCP`: single execution path that queries gateway scope over IPC before invoking sandbox bridges. | Chat, proposals, and persona prompt logic. |
| **`internal/sandboxmcp`** | Orchestrator client and catalog for the 54 sandbox-resident MCP and terminal session tools over persistent `docker exec -i`. | Direct shell pipelines. |
| **`internal/sandbox`** | `bridge.go`: executes unprivileged commands (`UID 1000:1000`) inside the Kali container with timeout and buffer caps. | Scope decisions and risk classification. |
| **`internal/gateway`** | Length-prefixed Unix IPC socket client connecting to the Rust gateway daemon. | Scope evaluation rationale. |
| **`internal/session`** | SQLite CRUD persistence for sessions, chat turns, and audit trails. | Execution mechanics. |
| **`internal/health`** | Ring-buffer telemetry metrics (memory, latency, goroutines) and health probes. | Business logic. |
| **`internal/llm`** | Ollama HTTP client and resilient circuit breaker. | Persona formatting. |
| **`internal/dnsresolve`** | Leaf resolver querying Cloudflare DNS (`1.1.1.1`) directly. | Network policy decisions. |

---

## The Leaf Packages (Zero Upstream Dependencies) <a id="leaf-packages"></a>

`internal/scope`, `internal/knowledge`, and `internal/security/threatscan` are leaf packages. They never import higher-level agent, conductor, or executor packages, preventing cyclic dependencies and keeping core data models isolated.

---

## The Conductor / Executor Boundary Rule <a id="where-things-plug-in"></a>

The boundary between `conductor` and `executor` is an inviolable architectural invariant:
- **`conductor`** understands AI reasoning, intent detection, proposals, and risk classification. It **never** executes commands or inspects network routes.
- **`executor`** understands network scope, gateway IPC, firewall rules, and container execution. It **never** parses prompts or manages chat history.
