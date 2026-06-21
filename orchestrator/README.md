# orchestrator — Orchestrator

orchestrator is the Go orchestrator for Yummi. It exposes the REST + WebSocket API, drives the AI conductor, manages the agentic task loop, and bridges commands to the Kali container (sandbox) through the trust boundary (gateway).

## Module

```
github.com/n0connect/yummi/orchestrator
```

## Quick start

```bash
# Run locally (requires gateway socket and sandbox container running, or degraded mode)
cd orchestrator && go run ./cmd/orchestrator

# Or via Makefile from repo root
make _build-orchestrator
./bin/yummi-orchestrator
```

Default listen: `0.0.0.0:9080`

## Package layout

```
cmd/orchestrator/             Binary entrypoint — wires all packages, starts HTTP server
internal/
  api/              HTTP & WebSocket layer (REST routes, SSE logs, WS events/terminal)
    handlers.go           Shared helpers: writeJSON, writeError, wsUpgrader
    handlers_session.go   Session / target / finding / audit CRUD
    handlers_execution.go Execution lifecycle (list, get, cancel)
    handlers_system.go    gateway + sandbox status and direct exec endpoint
    handlers_chat.go      Chat, proposal approval, events WebSocket
    routes.go             Chi mux route registration
    system.go             System info aggregator, log ring buffer, SSE endpoint
    terminal.go           PTY WebSocket bridge (docker exec → xterm.js)
    tasks.go              Agent task HTTP endpoints
  agent/            Agentic task loop (plan-then-execute + ReAct)
    runner.go    Loop orchestration — step budget, approval gating, stuck detection
    store.go     Task lifecycle store (SQLite) + event pub/sub
    task.go      TaskStatus state machine and Task struct
    gate.go      Blocking approval channel (medium+ risk commands pause here)
    exec.go      Adapter: agent commands → execution.Store
    prompts.go   LLM prompt templates and action JSON parser
  conductor/      AI assistant engine
    engine.go    LLM inference, persona loading, chat history
    validator.go Scope validation (IP/CIDR allow/block) and risk classification
    approval.go  In-memory proposal store with TTL
    tier.go      Ollama model tier registry (lite/pro/expert/standard)
    persona.go   Persona YAML loader
  execution/      Command execution lifecycle store
  executor/       Processor: execution.Store → gateway scope check → sandbox bridge
  session/        SQLite CRUD for sessions, targets, findings, audit trail
  llm/            Ollama HTTP client
  sandbox/             Docker exec bridge to Kali container
  gateway/          IPC client to gateway trust boundary daemon (Unix socket)
  health/         Ring-buffer health metrics + component pinger
  mcp/            JSON-RPC 2.0 stdio client for MCP servers
  tlsutil/        mTLS certificate generation (ECDSA P-256)
  config/         Environment-based configuration loader
```

## Key data flows

**Chat → single command:**
```
UI → POST /api/v1/chat
  → conductor.Engine.ChatWithHistory()
  → ChatResponse{intent:"suggest_command", proposals:[...]}
  → UI shows proposal bar
  → POST /api/v1/chat/approve
  → execution.Store.Enqueue()
  → executor.ProcessFunc → gateway.CheckScope → sandbox.Bridge.Exec
  → WS /ws/events fires execution.status events
```

**Chat → agentic task:**
```
UI → POST /api/v1/chat
  → conductor detects start_task intent
  → agent.Runner.CreateAndPlan() → LLM produces plan
  → agent.Runner.Start() → loop: step → LLM action → execute → observe → repeat
  → WS /ws/events fires task.status events
  → on completion, summary returned as chat reply
```

**Approval flow (medium+ risk commands):**
```
runner.runCommand() → validator risk ≥ medium
  → gate.Request() blocks the loop goroutine
  → task transitions to awaiting_approval
  → WS pushes task event with PendingCommand + PendingRisk
  → operator: POST /api/v1/tasks/{id}/approve
  → gate.Resolve(verdict=true) → loop resumes
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `YUMMI_ORCHESTRATOR_HOST` | `127.0.0.1` | Listen address |
| `YUMMI_ORCHESTRATOR_PORT` | `9080` | Listen port |
| `YUMMI_DB_PATH` | `~/.yummi/sessions.db` | SQLite database |
| `YUMMI_OLLAMA_URL` | _(empty)_ | Ollama endpoint; empty disables LLM |
| `YUMMI_OLLAMA_MODEL` | `qwen3:8b` | Model name (overridden by tier) |
| `YUMMI_OLLAMA_TIER` | _(empty)_ | `lite`/`pro`/`expert`/`standard` |
| `YUMMI_SANDBOX_CONTAINER` | `yummi-sandbox` | Kali container name |
| `YUMMI_GATEWAY_SOCKET` | `/run/yummi/gateway.sock` | gateway Unix socket path |
| `YUMMI_PERSONA_DIR` | `persona/v0.1` | Persona YAML directory |
| `PENTEST_SCOPE_ALLOW` | _(empty)_ | CSV of allowed IPs/CIDRs/domains |
| `PENTEST_SCOPE_BLOCK` | _(empty)_ | CSV of blocked IPs/CIDRs/domains |

## Testing

```bash
# All orchestrator tests
cd orchestrator && go test ./...

# Single package
cd orchestrator && go test ./internal/agent -run TestRunnerHappyPath
cd orchestrator && go test ./internal/conductor -run TestValidatorToolAllowlist
cd orchestrator && go test ./internal/api -run TestHandleChat

# With race detector
cd orchestrator && go test -race ./...
```

## Adding a new API endpoint

1. Add handler factory in the relevant domain file:
   - session/target/finding → `handlers_session.go`
   - execution lifecycle → `handlers_execution.go`
   - gateway/sandbox system → `handlers_system.go`
   - chat/agentic → `handlers_chat.go`
2. Register route in `routes.go`
3. Add test in `handlers_test.go` or `chat_test.go`

## Adding a new tool to the allowlist

1. `internal/conductor/validator.go` — add to `isToolAllowed`
2. `persona/v0.1/tool_knowledge.yaml` — add usage context
3. `internal/conductor/engine_test.go` — add intent test case
