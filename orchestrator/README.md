# orchestrator — the Go brain & API

> Quick local overview. For the full, kept-current reference (package map, conductor/executor boundary, agent internals), see [docs/modules/orchestrator.md](../docs/modules/orchestrator.md) and [docs/agent/](../docs/agent/).

The orchestrator owns everything AI/API/execution-related: the LLM **conductor**, the agentic **graph engine**, the **knowledge store** (world model + learning loop), and the REST + WebSocket API. It bridges commands to the Kali sandbox **only** through the gateway trust boundary — it never touches nftables and never shells out to the host on behalf of the model.

> Separate Go module: `github.com/n0connect/yummi/orchestrator`. Tests under `internal/*` must run from this directory (or with the `orchestrator/` prefix) — a different module than the repo-root CLI/TUI.

## Quick start

```bash
cd orchestrator && go run ./cmd/orchestrator      # local (gateway socket + sandbox, or degraded mode)
# or, from repo root:
make _build-orchestrator && ./bin/yummi-orchestrator
```

Default listen: `0.0.0.0:9080`.

## Package layout

```
cmd/orchestrator/        Binary entrypoint — wires every package, starts the HTTP server
internal/
  agent/                 Agentic GRAPH STATE MACHINE (not a flat loop)
    graph.go               Node set + graphEdges routing table + Execute() walk
    node_step.go           Ask the LLM for the next action; build SKILLS/MEMORY/DELEGATION preamble
    node_tool.go           Validate · gate · execute; duplicate / stuck / tool-thrash detection
    node_consult.go        Mid-command consult ([CONTINUE]/[ABORT])
    node_lifecycle.go      retry · approval · summarize · finish · ask nodes
    node_handoff.go        Delegate a subtask to a specialist sub-agent (depth-1)
    node_remember.go       Persist a structured memory ([REMEMBER]); threat-scanned
    protocol.go            Closed agent↔LLM tag vocabulary (Registry) + parser
    prompts.go             Prompt templates · observation compression
    store.go · task.go     SQLite task store + pub/sub · Task struct & status
    gate.go                Blocking operator-approval channel
    activity.go            Typed TRACE ActivityEvents
  knowledge/             World model (LEAF pkg) — single owner of all structured knowledge
    store.go               targets · findings · procedural_memory · persistent_context
    procedural.go          target-scrubbed tactic recording (the learning loop)
    retrieval.go           RetrievalStrategy — deterministic SQL recall (excludes current-task cmds)
  skill/                 Declarative skill registry (YAML playbooks, threat-scanned on load)
  security/              Content-layer fail-closed
    threatscan/            injection/exfil/C2/invisible-unicode scan + ANSI stripping
    invariants/            architectural fitness tests (exec-path, mcp-dormancy, no-telemetry)
  conductor/             AI engine
    engine.go              LLM inference · persona · per-session chat history
    validator.go           RISK CLASSIFICATION ONLY (no scope decisions, no tool allowlist)
    approval.go            In-memory proposal store with TTL
    tier.go · persona.go   Ollama tier presets · persona YAML loader
  scope/                 Single source of truth for ExtractTarget(command) (leaf)
  execution/ executor/   Execution lifecycle store · processor (scope check → sandbox)
  llm/                   Ollama HTTP client (+ Resilient: circuit breaker + tier-downgrade)
  sandbox/ gateway/      Docker exec bridge · IPC client to the gateway daemon
  session/ health/       SQLite session CRUD · ring-buffer health metrics
  api/                   REST routes · domain handlers · WS events/terminal · SSE logs
  mcp/                   JSON-RPC MCP client — DORMANT (host-exec; unimported by production)
  tlsutil/ config/ ui/   mTLS certs · env config · embedded ui/dist
```

## Key data flows

**Chat → agentic task** (the graph engine):

```text
   UI ──POST /api/v1/chat──▶ conductor
                              │ detect start_task intent
                              ▼
                        agent.Runner.CreateAndPlan()   (LLM produces a numbered plan)
                              │ Start() → Execute(state, nodeStep)
                              ▼
           ┌──────────────────────────────────────────────┐
           │ graph loop, until a terminal node:           │
           │   step → tool → step                         │
           │   (validate · scope · exec · observe)        │──▶ Knowledge Store
           │                                              │    (findings + target-scrubbed tactics)
           └──────────────────────────────────────────────┘
                              │
                              ▼
        task.status events over /ws/events  ·  summary returned as the chat reply
```

**Chat → single command** — `conductor.ChatWithHistory()` returns `suggest_command` proposals → `POST /chat/approve` → `execution.Store.Enqueue()` → `executor` (gateway `CheckScope` → `sandbox.Bridge.Exec`) → `execution.status` events.

**Approval gate** (operator sign-off): when the agent runs in `ask_permission` mode (or a single command needs approval), `gate.Request()` blocks the loop goroutine; the task transitions to `awaiting_approval`; the operator `POST`s `/tasks/{id}/approve`; `gate.Resolve()` resumes the loop. A 20-minute TTL purges abandoned gates.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `YUMMI_ORCHESTRATOR_HOST` / `_PORT` | `127.0.0.1` / `9080` | Listen address / port |
| `YUMMI_DB_PATH` | `~/.yummi/sessions.db` | SQLite database |
| `YUMMI_OLLAMA_URL` | _(empty)_ | Ollama endpoint; **empty = conductor declines everything (no rule-based fallback)** |
| `YUMMI_OLLAMA_MODEL` | `qwen3:8b` | Model name (overridden by tier) |
| `YUMMI_OLLAMA_TIER` | _(empty)_ | `lite` / `pro` / `expert` / `standard` |
| `YUMMI_OLLAMA_FALLBACK_MODEL` | _(empty)_ | Optional lighter LOCAL model the resilient client downgrades to |
| `YUMMI_SANDBOX_CONTAINER` | `yummi-sandbox` | Kali container name |
| `YUMMI_GATEWAY_SOCKET` | `/run/yummi/gateway.sock` | Gateway Unix socket |
| `YUMMI_PERSONA_DIR` | `persona/v0.1` | Persona YAML directory |

**Network scope is not an orchestrator concern.** It is configured exclusively in the gateway's own YAML (`scope.allow` / `scope.block`); the gateway is the sole scope authority. The validator does risk classification only.

## Testing

```bash
cd orchestrator && go test ./...                 # all packages
cd orchestrator && go test -race ./...           # concurrency-sensitive packages
cd orchestrator && go test ./internal/agent -run TestGraph_ToolThrash_SemanticDuplicateLoop
cd orchestrator && go test ./internal/security/invariants   # architectural fitness functions
```

**No mocks of Yummi's own components.** Tests exercise real stores/engines with in-memory SQLite; only the external LLM boundary is driven by a scripted client (`newFakeOllama`) and commands run in a real shell (`realShellStore`). `make test-agent-live` drives the real qwen3:8b + live sandbox end-to-end.

## Extending

- **Add an API endpoint** — handler in the matching domain file (`handlers_session.go` / `handlers_execution.go` / `handlers_system.go` / `handlers_chat.go`), register in `routes.go`, add a test.
- **Teach the agent a new tool** — there is **no tool allowlist to edit**. The model relies on its own pretrained knowledge; for a niche tool, add a short usage note to `persona/v0.1/core_identity.yaml` or `agent.yaml`. Special-case `classifyRisk` in `validator.go` only if the tool family needs a structural risk floor.
- **Add a skill / sub-agent role** — drop a YAML file in `persona/v0.1/skills/` or `persona/v0.1/subagents/`; no code change.
- **Swap the brain** — implement the one-method `LLM` interface; the graph engine is brain-agnostic and never changes.
