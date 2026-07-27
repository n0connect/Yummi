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
| **`internal/agent`** | The graph state machine, tag protocol, gate, retry/verify logic, sub-agent hand-off. See [Agent](../agent/) for the full behavioral deep-dive. | How a command actually runs (`exec.go` only enqueues) |
| **`internal/api`** | REST routes, WS bridges (`terminal.go`), SSE log stream. Handlers split by domain: `handlers_session.go`, `handlers_execution.go`, `handlers_gateway.go`, `handlers_system.go`, `handlers_chat.go`, `handlers_trace.go`, `handlers_skills.go` | Chat/LLM internals, gateway IPC wire format, sandbox exec mechanics — it calls into the packages that own those |
| **`internal/conductor`** | `engine.go` (LLM chat + persona), `validator.go` (risk classification **only** — no scope logic, see below), `approval.go` (TTL proposal store), `tier.go` (model presets), `persona.go` (YAML loader) | Network scope (removed entirely — see [Security Model](../security-model.md)); how commands run |
| **`internal/execution`** | The command execution lifecycle store — queued → running → done/failed, `EgressApprovalRequiredError` | Whether a command *should* run (that's the executor's job) |
| **`internal/executor`** | `RunScopedCommand` — the single gateway-scope-checked exec path. See [Sandbox Execution](../agent/sandbox-execution.md) for the full chain. | Chat, proposals, personas — anything conductor-side |
| **`internal/scope`** | `ExtractTarget(command)` — the single source of truth for "which token in this command is the network target," shared by the conductor's classifier and the executor's scope check so they always agree | Whether a target is *allowed* — that's gateway's call entirely |
| **`internal/gateway`** | The IPC client to the gateway daemon (Unix socket, length-prefixed JSON), plus `testserver.go` — a real Unix-socket wire-protocol test fixture (not a mock; see [Development § Linting](../development.md#lint-vuln) on this project's no-mocks policy) | Anything about *why* a scope decision was made — it just asks and relays the answer |
| **`internal/knowledge`** | Targets, findings, tactics, persistent context — the single world-model owner. See [Memory & Knowledge](../agent/memory-and-knowledge.md). | — |
| **`internal/skill`**, **`internal/skillcurator`** | Declarative skill loading/matching; the auto-skill-drafting pipeline and its quarantine store. See [Skills & Sub-agents](../agent/skills-and-subagents.md). | — |
| **`internal/security/threatscan`** | The content-layer pattern registry — every prompt-injection/C2/exfiltration pattern in one auditable file. See [Security Model § Content layer](../security-model.md#layer-content). | Network scope, execution — pure content-layer, zero internal deps by design |
| **`internal/security/invariants`** | Structural tests asserting the execution invariants described in [Sandbox Execution](../agent/sandbox-execution.md) actually hold — `TestExecImportAllowlist` fails the build if a new `os/exec` import appears outside an explicit allowlist of audited files, and a second, narrower check specifically forbids `internal/agent` from importing `os/exec` at all | — |
| **`internal/sandbox`** | `bridge.go` — the literal `docker exec` call, 120s timeout, 1 MiB output cap | Scope, risk, personas — it just runs what it's told and reports the result |
| **`internal/session`** | SQLite CRUD for sessions/targets/findings/audit | — |
| **`internal/health`** | Ring-buffer runtime metrics + component pinger (Docker, gateway, sandbox, Ollama), `GatewayEvent` diffing for the Gateway tab's live updates | — |
| **`internal/llm`** | Ollama HTTP client (`client.go`), embedding calls (`embed.go` — backs [semantic memory retrieval](../agent/memory-and-knowledge.md#retrieval)), a resilience wrapper (`resilient.go`) | Personas, prompts — pure transport |
| **`internal/dnsresolve`** | Resolves hostnames via Cloudflare (1.1.1.1), the same resolver the sandbox container itself is configured to use | Scope decisions — resolution only maps name→IP, gateway still decides reachability |
| **`internal/mcp`** | A JSON-RPC 2.0 stdio client for external MCP servers — real, tested, **dormant** (not imported by any production path today; see [Architecture § What is not in this repository](../architecture.md#not-in-scope)) | — |
| **`internal/tlsutil`** | mTLS certificate generation (ECDSA P-256), for the optional `YUMMI_GATEWAY_TLS` path | — |
| **`internal/config`** | Env var loading (`YUMMI_*`) | — |

## The leaf packages — deliberately dependency-free <a id="leaf-packages"></a>

`internal/scope`, `internal/knowledge`, and `internal/security/threatscan` share a specific architectural discipline worth naming: each is a **leaf package** — it depends only on the standard library (plus, for `knowledge`, `database/sql`), never on `agent`, `conductor`, or `executor`. This is what lets packages on opposite sides of the conductor/executor boundary (see below) both import the same leaf without creating an import cycle, and it's why, for example, `scope.ExtractTarget` is called identically by both the conductor's risk classifier and the executor's scope check — there is only one implementation to possibly disagree with itself.

## Where a new feature usually plugs in <a id="where-things-plug-in"></a>

The **conductor/executor boundary** is the single most important thing to internalize before extending this module, because it's enforced as a design rule, not just a suggestion: `conductor` knows about the LLM, personas, proposals, risk levels — and **never** about how commands actually run. `executor` knows about gateway/sandbox/the execution store — and **never** about chat or proposals. A new feature that seems to need "a bit of both" is usually a sign it should be split across this boundary rather than merging the two packages' concerns — keeping it intact is what lets either side be modified or even swapped independently (e.g. a different LLM backend would only ever touch `conductor`/`llm`, never `executor`).
