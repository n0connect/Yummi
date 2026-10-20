# orchestrator — the Go Brain & API

> For the full, kept-current reference, see [docs/modules/orchestrator.md](../docs/modules/orchestrator.md) and [docs/agent/](../docs/agent/).

The orchestrator owns everything AI/API/execution-related: the LLM **conductor**, the agentic **graph engine**, the **knowledge store** (world model, ASM matrix, learning loop), the **reporting engine**, and the REST + WebSocket API. It bridges commands to the Kali sandbox **only** through the gateway trust boundary — it never touches nftables directly and never shells out to the host on behalf of the model.

> Separate Go module: `github.com/n0connect/yummi/orchestrator`. Tests under `internal/*` must run from this directory (or with the `orchestrator/` prefix) — a different module than the repo-root CLI.

---

## Quick Start

```bash
cd orchestrator && go run ./cmd/orchestrator      # local (gateway socket + sandbox, or degraded mode)
# or, from repo root:
make _build-orchestrator && ./bin/yummi-orchestrator
```

Native default listen: `127.0.0.1:9080`. Compose binds the main API to
`0.0.0.0:9080` inside the container and publishes no orchestrator port.
`OperatorAccess` checks the actual socket peer: only loopback and the exact
UI control address `172.21.0.4/32` are operator peers in Compose; spoofed
forwarded headers confer no authority. Host loopback ports `3000` and `9080`
both terminate at UI nginx, which proxies API/WebSocket traffic over the
internal control network.

The orchestrator joins private `172.20.0.0/24` at `.3`, internal control
`172.21.0.0/24` at `.3`, and external egress. Sandbox policy reads use the
separate `172.20.0.3:9081` listener: hostname policy and literal-IP egress
checks only, with no mutation/approval/session routes. The constrained Docker
broker is control-only at `172.21.0.5`; the UI is `.4`. Only the UI joins the
normal `172.22.0.0/24` ingress bridge. Isolated OrbStack requests through the
published ports appeared to nginx as exactly `172.22.0.1`, not an arbitrary
trusted subnet. See [network topology](../docs/architecture.md#network-topology).

---

## Package Layout

```text
cmd/orchestrator/        Binary entrypoint — wires every package, starts the HTTP server
internal/
  agent/                 Agentic GRAPH STATE MACHINE (not a flat loop)
    graph.go               Node set + graphEdges routing table + Execute() walk
    node_step.go           Ask the LLM for the next action; build SKILLS/MEMORY/DELEGATION preamble
    node_tool.go           Validate, gate, execute; duplicate / stuck / tool-thrash detection
    node_consult.go        Mid-command consult ([CONTINUE]/[ABORT])
    node_lifecycle.go      retry, approval, summarize, finish, ask nodes
    node_handoff.go        Delegate one or more subtasks concurrently (depth-1)
    node_remember.go       Persist a structured memory ([REMEMBER]); threat-scanned
    protocol.go            Closed agent-LLM tag vocabulary (Registry) + parser
    prompts.go             Prompt templates and observation compression
    store.go, task.go      SQLite task store + pub/sub; Task struct & status
    gate.go                Blocking operator-approval channel
    activity.go            Typed TRACE ActivityEvents
    oracle.go, verdict.go  Output-based evidence heuristics (ActivePoC, DOM, OOB, Diff)
    evaluation.go          Objective quality metrics and assertion gates
    eino/                  Local or configured remote Eino tool-calling backend; discovers sandbox schemas
  knowledge/             World model (LEAF pkg) — single owner of all structured knowledge
    store.go               targets, findings, procedural_memory, persistent_context, assets, scope_rules
    assets.go              Hierarchical Attack Surface Management (ASM) tree matrix
    program_scope.go       Bug-bounty in-scope/out-of-scope program rules & live classifier
    procedural.go          target-scrubbed tactic recording (the learning loop)
    retrieval.go           RetrievalStrategy — structured SQL recall (excludes current-task cmds)
    semantic_retrieval.go  Local Ollama embeddings with structured fallback
  report/                Multi-format Pentest & Bug Bounty Report Generator (H1, VRT, Exec, JSON, program-scope warnings)
  skill/                 Declarative YAML skill registry; threat-scanned on load
  skillcurator/          Autonomous skill creation from successful trajectories (quarantine store)
  security/              Content-layer fail-closed
    threatscan/            injection/exfil/C2/invisible-unicode scan + ANSI stripping
    invariants/            architectural fitness tests (exec-path, mcp-dormancy, no-telemetry)
  conductor/             AI engine
    engine.go              LLM inference, persona, per-session chat history
    validator.go           RISK CLASSIFICATION ONLY (no scope decisions, no tool allowlist)
    approval.go            In-memory proposal store with TTL
    tier.go, persona.go    Ollama tier presets (standard / expert), persona YAML loader
    golden.go, policy.go   Deterministic persona boundary fixtures and policy
  scope/                 Single source of truth for ExtractTargets(command) (leaf)
  execution/ executor/   Execution lifecycle store (rationale-aware) and processor (scope check -> sandbox)
  llm/                   Ollama HTTP client (+ Resilient: circuit breaker + tier-downgrade)
  sandbox/ gateway/      Docker exec bridge, IPC client to the gateway daemon
  sandboxmcp/            Persistent Docker MCP client; discover installed tool schemas
  retention/             Background retention cleaner for aged session data
  session/ health/       SQLite session CRUD, ring-buffer health metrics
  api/                   REST routes, domain handlers (assets, scope rules, report, proxy), WS events/terminal
  mcp/                   JSON-RPC MCP client — DORMANT (host-exec; unimported by production)
  tlsutil/ config/       TLS scaffolding and environment config (UI is a separate deployable)
```

---

## Key Data Flows

### Chat -> Agentic Task (The Graph Engine):

```text
   UI --- POST /api/v1/chat ---> conductor
                                     |
                                     | (detects start_task intent)
                                     v
                               agent.Runner.CreateAndPlan()
                                     |
                                     | Start() -> Execute(state, nodeStep)
                                     v
            +-------------------------------------------------------------+
            | Graph State Machine Loop (until finish / ask):              |
            |   step -> tool -> step                                      |
            |   (validate -> scope check -> exec -> oracle verify)        |---> Knowledge Store
            |                                                             |     (Findings, Assets, Memory)
            +-------------------------------------------------------------+
                                     |
                                     v
         task.status events over /ws/events; summary persisted in session chat
```

### Chat -> Single Command:
`conductor.ChatWithHistory()` returns `suggest_command` proposals -> `POST /chat/approve` -> `execution.Store.Enqueue()` -> `executor` (`scope.ExtractTargets` -> hostname policy + DNS -> gateway egress verdict for every target -> temporary rules only after every target is allowed -> `sandbox.Bridge.Exec`) -> `execution.status` events.

---

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `YUMMI_ORCHESTRATOR_HOST` / `_PORT` | `127.0.0.1` / `9080` | Listen address / port |
| `YUMMI_DB_PATH` | `~/.yummi/sessions.db` | SQLite database |
| `YUMMI_OLLAMA_URL` | _(empty)_ | Ollama endpoint; empty + no compatible endpoint override = conductor declines |
| `YUMMI_OLLAMA_MODEL` | `gpt-oss:20b` | Explicit model tag; takes precedence over a tier |
| `YUMMI_AGENT_BACKEND` | `graph` | Execution engine (`graph` state machine or `eino` tool calling) |
| `YUMMI_DATA_RETENTION_DAYS` | `0` | Auto-pruning retention horizon for historical executions |

See the [configuration reference](../docs/configuration.md) for model routing, native versus Compose defaults, settings not forwarded into containers and generated-skill storage.
