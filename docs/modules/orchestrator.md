[Back to Project README](../../README.md) · [Docs index](../README.md)

# Module: orchestrator (Go)

This page maps `orchestrator/` packages by **architectural boundary** — what each component owns, and what it deliberately does not touch.

`orchestrator/go.mod` (`github.com/n0connect/yummi/orchestrator`) is an independent Go module, isolated from root CLI and `sandbox-mcp`.

---

## Table of Contents

- [Entry Point](#entry-point)
- [Package Map](#package-map)
- [Lower-Level Shared Packages](#leaf-packages)
- [The Conductor / Executor Boundary Rule](#where-things-plug-in)

---

## Entry Point <a id="entry-point"></a>

`cmd/orchestrator/main.go` wires the operator router and the separate read-only
sandbox policy listener. Compose publishes neither listener directly: host
loopback ports 3000/9080 go through UI nginx on the UI-only ingress bridge.
The main API uses exact socket-peer authorization on internal control;
`172.20.0.3:9081` exposes policy checks without operator routes. See
[network topology](../architecture.md#network-topology). Dependencies are
instantiated explicitly without reflection or magic containers.

---

## Package Map <a id="package-map"></a>

| Package | Responsibility | Deliberately Excluded Concerns |
|---|---|---|
| **`internal/agent`** | Defines `TaskRunner` lifecycle interface, graph state machine (`graph.go`), tag protocol (`protocol.go`), gates (`gate.go`), output-based verification heuristics (`oracle.go`), and delegation (`node_handoff.go`). | Command execution mechanics (`exec.go` strictly enqueues). |
| **`internal/agent/eino`** | Alternate `TaskRunner` implementation using CloudWeGo Eino ADK for native tool-calling on larger models. Discovers sandbox MCP tools (typed/cataloged wrappers, recon utilities, tmux terminals, browser automation, and research), and reuses `Execer`, `Verify`, `Gate`, `threatscan`, and `conductor.Validator`. | Process execution mechanics (uses orchestrator executor). |
| **`internal/api`** | REST routes, WebSocket bridges (`terminal.go`, `desktop.go`), and SSE logs. Handlers organized by domain (`handlers_session.go`, `handlers_assets.go`, `handlers_report.go`, `handlers_proxy.go`, `handlers_gateway.go`, `handlers_system.go`, `handlers_scope.go`). | LLM internals and packet-policy enforcement. |
| **`internal/conductor`** | AI conductor engine (`engine.go`), risk classification floor (`validator.go`), TTL proposal store (`approval.go`), model tiers (`tier.go`), and persona fixtures (`golden.go`). | Network firewall rules and command execution. |
| **`internal/knowledge`** | World model: targets, security findings, procedural tactics, persistent context, Attack Surface Management (`assets.go`), and program scope rules (`program_scope.go`). | Firewall manipulation and execution dispatch. |
| **`internal/report`** | Multi-Format Reporting Engine: compiles session data into HackerOne Markdown, Bugcrowd VRT Markdown, Executive Management summaries, and JSON with program-scope classification/warnings. | Session persistence and network execution. |
| **`internal/skill`** | Declarative YAML offensive playbooks; content threat-scanned on load. | Direct binary execution. |
| **`internal/skillcurator`** | Autonomous skill creation: sanitizes successful trajectories (`sanitize.go`) and quarantines drafts for operator review. | Live activation without operator consent. |
| **`internal/security/threatscan`** | Content-layer prompt-injection, exfiltration, C2, and unicode scanner. | Network rules and process spawning. |
| **`internal/security/invariants`** | AST-walking fitness function tests locking execution invariants in CI. | Production runtime operations. |
| **`internal/scope`** | Single source of truth for `ExtractTargets(command)` across IPs, CIDRs, and URLs. | Reachability verdicts (sole authority is gateway). |
| **`internal/execution`** | Execution lifecycle store: state transitions (`queued` -> `running` -> `completed` / `failed`), bounded result capture and optional live output chunks, rationale tracking (`execution.ContextWithRationale`), and heartbeat ticks. | Policy decisions. |
| **`internal/executor`** | `RunScopedCommand` and `processMCP`: single execution path that queries gateway scope over IPC before invoking sandbox bridges. | Chat, proposals, and persona prompt logic. |
| **`internal/sandboxmcp`** | Orchestrator client and catalog for sandbox-resident MCP, browser, research, and terminal session tools over persistent `docker exec -i`. | Direct shell pipelines. |
| **`internal/sandbox`** | `bridge.go`: executes commands as the non-root user (`UID 1000:1000`; selected tool file capabilities remain effective) inside the Kali container with timeout and buffer caps. | Scope decisions and risk classification. |
| **`internal/gateway`** | Length-prefixed Unix IPC socket client connecting to the Rust gateway daemon. | Scope evaluation rationale. |
| **`internal/session`** | SQLite CRUD persistence for sessions, chat turns, and audit trails. | Execution mechanics. |
| **`internal/health`** | Ring-buffer telemetry metrics (memory, latency, goroutines) and health probes. | Business logic. |
| **`internal/llm`** | Ollama chat/embedding client and resilient circuit breaker; conductor can also use an explicitly configured compatible endpoint. | Persona formatting. |
| **`internal/dnsresolve`** | Leaf resolver querying Cloudflare DNS (`1.1.1.1`) directly. | Network policy decisions. |

---

## Lower-Level Shared Packages <a id="leaf-packages"></a>

`internal/scope`, `internal/knowledge`, and `internal/security/threatscan` are leaf packages. They avoid dependencies on agent/conductor/executor, preventing cyclic dependencies and keeping core data models isolated. “Leaf” here describes that layering, not a claim of zero dependencies on lower-level helpers or libraries.

---

## The Conductor / Executor Boundary Rule <a id="where-things-plug-in"></a>

The boundary between `conductor` and `executor` is an inviolable architectural invariant:
- **`conductor`** understands AI reasoning, intent detection, proposals, and risk classification. It **never** executes commands or inspects network routes.
- **`executor`** understands network scope, gateway IPC, firewall rules, and container execution. It **never** parses prompts or manages chat history.

## Model endpoints and privacy

Local Ollama is the default. An explicit `YUMMI_EINO_MODEL_BASE_URL` selects a compatible endpoint for conductor/chat and the primary agent inference path, including the graph backend. Eino supports configured provider selection (`ollama`, `openrouter`, `openai`); a compatible endpoint can also be another local server. Remote endpoints receive the prompt/context sent to them. The optional embedding path remains separately configured through Ollama. Research HTTP/DNS/provider credentials and parsing stay inside sandbox-MCP; the orchestrator does not implement a host web-search client.
