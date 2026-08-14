← [Back to README](../../README.md) · [Docs index](../README.md)

# Module: orchestrator (Go)

This page is a map of `orchestrator/`'s packages by **implementation boundary** — what each one owns and, more importantly, what it deliberately does *not* know about. If you're looking for *behavior* (how the agent reasons, how memory retrieval works, how a command gets scope-checked), see [Agent](../agent/) — this page is for *"which file do I open"* orientation, not mechanism.

`orchestrator/go.mod` (`github.com/n0connect/yummi/orchestrator`) is its own Go module, separate from the CLI/TUI's root `go.mod` — see [Architecture § Build ecosystems](../architecture.md#build-ecosystems) for why, and [Development § Two Go modules](../development.md#two-go-modules) for the practical `cd orchestrator` consequence.

## Table of contents

- [Entry point](#entry-point)
- [Package map](#package-map)
- [The leaf packages — deliberately dependency-free](#leaf-packages)
- [Where a new feature usually plugs in](#where-things-plug-in)

---

## Entry point <a id="entry-point"></a>

`cmd/orchestrator/main.go` wires every package below into the Chi router (`internal/api/routes.go`) and starts the HTTP/WS server. There is no dependency-injection framework — construction is one explicit chain of `New(...)` calls in `main`, readable top to bottom.

## Package map <a id="package-map"></a>

| Package | Owns | Explicitly does NOT know about |
|---|---|---|
| **`internal/agent`** | The `TaskRunner` interface (`task_runner.go`) — the full lifecycle surface `internal/api` depends on — plus its default implementation: the graph state machine, tag protocol, gate, retry/verify logic, concurrent sub-agent hand-off, objective task-quality evaluator, and server-side finding verdicts. See [Agent](../agent/) for the full behavioral deep-dive. | How a command actually runs (`exec.go` only enqueues) |
| **`internal/agent/eino`** | A second `TaskRunner` implementation on [CloudWeGo's Eino ADK](https://github.com/cloudwego/eino), for native tool-calling against larger/more capable local models — selected instead of the graph engine via `YUMMI_AGENT_BACKEND=eino` (`make conf`, default `graph`). Reuses the graph engine's own `Execer`, `Verify`, `threatscan`, `Gate`, and `conductor.Validator` unchanged — same single exec path, same approval gating, same content-layer scan, just a different LLM-facing loop. Full agent/memory/skill parity with the graph backend: `Config.Retrieval`/`Config.Skills` fold into the task's `Instruction` once at `Run()` start; a `handoff` tool delegates to the same graph-based specialist `Runner`s and `DelegationCoordinator` the graph backend uses; `finish`/`ask_operator` are `ReturnDirectly` tools that end the ReAct turn immediately (`ask_operator` pauses via `StatusPaused`, and a `Start()` resume folds the operator's answer into the next run); `Config.SkillCurator` fires auto-skill-creation from a tracked trajectory; `StepBudget` genuinely bounds the loop via `MaxIterations`. Three-tier pentest tool strategy backed by `internal/sandboxmcp`/`agent.MCPExecer`: six individually-typed tools (`nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`) via one generic `pentestTool` type; `search_pentest_tools`/`run_pentest_tool` for the rest of the sandbox-mcp catalog via MCP's `tools/list` discovery, sharing the same `runPentestCall` pipeline; `run_command` as the free-text fallback. Still graph-only: stuck/duplicate/tool-thrash loop-prevention heuristics and checkpoint/crash-recovery. | How a command actually runs (same `Execer`/`MCPExecer` the graph engine's execution pipeline provides) |
| **`internal/api`** | REST routes, WS bridges (`terminal.go`), SSE log stream. Handlers split by domain: `handlers_session.go`, `handlers_execution.go`, `handlers_gateway.go`, `handlers_system.go`, `handlers_chat.go`, `handlers_trace.go`, `handlers_skills.go` | Chat/LLM internals, gateway IPC wire format, sandbox exec mechanics — it calls into the packages that own those |
| **`internal/conductor`** | `engine.go` (LLM chat + persona), `validator.go` (risk classification **only** — no scope logic, see below), `approval.go` (TTL proposal store), `tier.go` (model presets), `persona.go` (YAML loader), `golden.go`/`policy.go` (deterministic boundary fixtures) | Network scope (removed entirely — see [Security Model](../security-model.md)); how commands run |
| **`internal/execution`** | The command execution lifecycle store — `queued` → `running` → `completed`/`failed`/`canceled`, plus two operator-gated holds: `awaiting_egress_approval` (`ErrEgressApprovalRequired`) and `awaiting_threat_approval` (a flagged single-command result held until `ResolveThreatApproval`, see [Security Model § Content layer](../security-model.md#layer-content)). Also republishes `execution.running` on a heartbeat (`runWithHeartbeat`, `ElapsedSecs`) while a long command is still in flight | Whether a command *should* run (that's the executor's job) |
| **`internal/executor`** | `RunScopedCommand` — the single gateway-scope-checked exec path for shell commands, plus `processMCP` — the equivalent path for MCP-tagged execution records (Eino's pentest tools), sharing the same `checkTargetScope`/`runScopedTarget` gateway decision logic. See [Sandbox Execution § MCP-backed pentest tools](../agent/sandbox-execution.md#mcp). | Chat, proposals, personas — anything conductor-side |
| **`internal/sandboxmcp`** | The orchestrator-side client for the MCP server running inside the sandbox container — a persistent session reached via `docker exec -i` (same invocation shape as `internal/sandbox`, held open instead of one-shot), `Client.CallTool` for dispatch and `Client.ListTools` for discovery (MCP's own `tools/list`), plus the per-tool target-field/display-rendering catalog for the twenty cataloged pentest tools. | Chat, proposals, personas |
| **`internal/scope`** | `ExtractTargets(command)` — the single source of truth for explicit IP/hostname/URL targets in a command; `ExtractTarget` remains a compatibility helper for representative-target callers | Whether a target is *allowed* — that's gateway's call entirely |
| **`internal/gateway`** | The IPC client to the gateway daemon (Unix socket, length-prefixed JSON), plus `testserver.go`, a scripted Unix-socket protocol fixture for tests | Anything about *why* a scope decision was made — it just asks and relays the answer |
| **`internal/knowledge`** | Targets, findings, tactics, persistent context — the single world-model owner. See [Memory & Knowledge](../agent/memory-and-knowledge.md). | — |
| **`internal/skill`**, **`internal/skillcurator`** | Declarative skill loading/matching; the auto-skill-drafting pipeline and its quarantine store. See [Skills & Sub-agents](../agent/skills-and-subagents.md). | — |
| **`internal/security/threatscan`** | The content-layer pattern registry — every prompt-injection/C2/exfiltration pattern in one auditable file. See [Security Model § Content layer](../security-model.md#layer-content). | Network scope, execution — pure content-layer, zero internal deps by design |
| **`internal/security/invariants`** | Structural tests asserting the execution invariants described in [Sandbox Execution](../agent/sandbox-execution.md) actually hold — `TestExecImportAllowlist` fails the build if a new `os/exec` import appears outside an explicit allowlist of audited files, narrower checks specifically forbid both `internal/agent` and `internal/agent/eino` from importing `os/exec` at all (the latter locks the Eino backend to the same single-exec-path invariant), and `TestSandboxMCPOnlyExecsDocker` AST-walks `internal/sandboxmcp` to guarantee its one allowlisted `os/exec` import can only ever run the literal `"docker"` binary | — |
| **`internal/sandbox`** | `bridge.go` — the literal `docker exec` call, a 10-minute default timeout (`SetTimeout`, overridable via `YUMMI_SANDBOX_EXEC_TIMEOUT`), 1 MiB output cap | Scope, risk, personas — it just runs what it's told and reports the result |
| **`internal/session`** | SQLite CRUD for sessions, chat messages, and session audit entries. Targets/findings belong to `internal/knowledge` | — |
| **`internal/health`** | Ring-buffer runtime metrics + component pinger (Docker, gateway, sandbox, Ollama), `GatewayEvent` diffing for the Gateway tab's live updates | — |
| **`internal/llm`** | Ollama HTTP client (`client.go`), embedding calls (`embed.go` — backs [semantic memory retrieval](../agent/memory-and-knowledge.md#retrieval)), a resilience wrapper (`resilient.go`) | Personas, prompts — pure transport |
| **`internal/dnsresolve`** | Resolves hostnames via Cloudflare (1.1.1.1), the same resolver the sandbox container itself is configured to use | Scope decisions — resolution only maps name→IP, gateway still decides reachability |
| **`internal/mcp`** | A JSON-RPC 2.0 stdio client for external MCP servers — real, tested, **dormant** (not imported by any production path today; see [Architecture § What is not in this repository](../architecture.md#not-in-scope)) | — |
| **`internal/tlsutil`** | ECDSA P-256 certificate generation for an opt-in gateway-TLS path. The Rust IPC server currently supports Unix-socket framing only, so this is dormant infrastructure rather than an end-to-end transport option | — |
| **`internal/config`** | Env var loading (`YUMMI_*`) | — |

## The leaf packages — deliberately dependency-free <a id="leaf-packages"></a>

`internal/scope`, `internal/knowledge`, and `internal/security/threatscan` sit below the agent/conductor/executor packages and do not import them. That direction keeps target extraction, world-model storage, and content scanning reusable without cycles. Today `scope.ExtractTargets` is consumed by execution/API paths; the conductor validator classifies command risk and does not perform target extraction or network-scope decisions.

## Where a new feature usually plugs in <a id="where-things-plug-in"></a>

The **conductor/executor boundary** is the single most important thing to internalize before extending this module, because it's enforced as a design rule, not just a suggestion: `conductor` knows about the LLM, personas, proposals, risk levels — and **never** about how commands actually run. `executor` knows about gateway/sandbox/the execution store — and **never** about chat or proposals. A new feature that seems to need "a bit of both" is usually a sign it should be split across this boundary rather than merging the two packages' concerns — keeping it intact is what lets either side be modified or even swapped independently (e.g. a different LLM backend would only ever touch `conductor`/`llm`, never `executor`).
