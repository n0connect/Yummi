<div align="center">

# Yummi

**A privacy-first, fail-closed AI pentest conductor.**

Yummi turns natural language into practical security workflows while keeping traffic control and operator authority explicit. An autonomous Red-Team brain drives a hardened, network-isolated Kali sandbox — and a memory-safe Rust trust boundary makes sure the AI can never touch a target it was not allowed to.

![CI](https://img.shields.io/badge/CI-pending-lightgrey)
![License](https://img.shields.io/badge/license-MIT-blue)
![Version](https://img.shields.io/badge/version-v0.1.0-lightgrey)
![Go](https://img.shields.io/badge/Go-1.22-00ADD8)
![Rust](https://img.shields.io/badge/Rust-stable-DEA584)
![React](https://img.shields.io/badge/React-18-61DAFB)

</div>

---

## Table of contents

- [What Yummi is](#what-yummi-is)
- [System architecture](#system-architecture)
- [The trust boundary (the single most important invariant)](#the-trust-boundary)
- [The agent engine](#the-agent-engine)
  - [Graph state machine — nodes & edges](#graph-state-machine)
  - [The tag protocol](#the-tag-protocol)
  - [Brain-agnostic core](#brain-agnostic-core)
- [The Knowledge Store — two-layer memory + learning loop](#the-knowledge-store)
- [Skills — declarative just-in-time playbooks](#skills)
- [Sub-agents — bounded delegation](#sub-agents)
- [TRACE — agent observability](#trace-observability)
- [The conductor, validator & egress modes](#conductor-validator-egress)
- [Gateway (Rust trust boundary)](#gateway)
- [Sandbox](#sandbox)
- [Interfaces — Web UI & TUI](#interfaces)
- [Quick start](#quick-start)
- [Developer workflow](#developer-workflow)
- [Configuration](#configuration)
- [Repository layout](#repository-layout)
- [Security invariants](#security-invariants)

---

## What Yummi is <a id="what-yummi-is"></a>

Yummi is **four components and one alternative front-end**, built around a hard rule: *the AI proposes, the gateway disposes.* Every pentest command the AI runs flows through exactly one place and is gated by a fail-closed network policy that survives crashes.

```
User / Operator  →  Orchestrator (Go)  →  Gateway (Rust, IPC)  →  Sandbox (Kali Docker)
  Web UI / TUI       conductor + agent      fail-closed nftables     tool execution only
                     knowledge + API        policy engine + audit
```

- **orchestrator** (Go) — owns the AI conductor, the agentic graph engine, the knowledge store, and the REST/WebSocket API. Never touches nftables.
- **gateway** (Rust, binary `yummi-gateway`) — the only component that can open external traffic from the sandbox. Installs `DROP`-all nftables rules on startup; if it crashes, those rules persist (**fail-closed**).
- **sandbox** (Kali Docker) — read-only fs, non-root, only `NET_RAW`/`NET_ADMIN`. Passive: it only acts when the orchestrator runs `docker exec` into it.
- **ui** (React 18 + Vite) — web dashboard on `:3000`, proxying `/api/` and `/ws/` to the orchestrator on `:9080`.
- **TUI** (Charm Bubble Tea) — a fully terminal-based operator console (`yummi --tui`), talking to the same REST/WS API.

---

## System architecture <a id="system-architecture"></a>

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│                              User / Operator                                  │
│                        Web Dashboard  ·  Terminal TUI                         │
└───────────────────────────────────────┬──────────────────────────────────────┘
                                        │  REST  +  WebSocket  (:9080)
┌───────────────────────────────────────▼──────────────────────────────────────┐
│                              Orchestrator (Go)                                │
│                                                                              │
│   conductor ──▶ agent graph engine ──▶ execution store ──▶ executor          │
│   (LLM, persona)   (node state machine)   (queue + events)   (scope + exec)   │
│        │                  │                                       │           │
│        └──────────▶ Knowledge Store ◀── Skills · Sub-agents · TRACE           │
│              (targets · findings · procedural memory · context · activity)    │
└───────────────────────────────────────┬──────────────────────────────────────┘
                                        │  Unix domain socket (length-prefixed JSON IPC)
┌───────────────────────────────────────▼──────────────────────────────────────┐
│                              Gateway (Rust)                                   │
│      fail-closed nftables · egress modes · SHA-256 audit chain · watchdog     │
└────────────────────┬───────────────────────────────────┬─────────────────────┘
                     │  docker exec (only path)           │  controlled egress
      ┌──────────────▼────────────┐          ┌────────────▼───────────┐
      │   Sandbox (Kali Docker)   │          │    External network    │
      │  read-only · non-root     │          │  (per gateway policy)  │
      └───────────────────────────┘          └────────────────────────┘
```

### Network topology

```
yummi_internal (172.20.0.0/24): sandbox .10 · gateway .2 (also DNS) · orchestrator .3 · ui .4
yummi_external:                 gateway · orchestrator · ui only — the sandbox is intentionally absent.
                                Its only path out is through the gateway's nftables rules.
Host ports: 3000 → ui nginx · 9080 → orchestrator API (localhost only)
```

DNS for the sandbox is forced through the gateway (`172.20.0.2`), so every lookup is logged.

---

## The trust boundary <a id="the-trust-boundary"></a>

This is the **single most important invariant** in the codebase: *deny until explicitly allowed.*

- The gateway installs `DROP`-all nftables rules **before** its IPC socket even opens. The sandbox has zero connectivity until an explicit `Allow` rule lands.
- If the gateway process dies, the `DROP` rules remain in the kernel — the system fails **closed**, never open.
- **Block rules always beat Allow rules.**
- Pentest commands reach a shell through **exactly one place**: `sandbox.Bridge.Exec`, which runs `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`. The agent never calls `exec.Command` itself — it only enqueues into the execution store, which the executor drains (gateway scope check → sandbox exec). No host-side command path is ever reachable from agent/LLM output.

> The gateway is Linux/nftables-only. It compiles on macOS (it shells out to `nft`) but its enforcement only works on Linux — local macOS dev is **not** a real isolation boundary.

---

## The agent engine <a id="the-agent-engine"></a>

Yummi's agent is **not a flat `while` loop**. It is an explicit **graph state machine**: the LLM emits a single tagged action per turn, and a deterministic router walks typed nodes via a fixed edge table until a terminal node is reached. This design tames context-window exhaustion and infinite action loops — each step re-renders the full task state into the prompt rather than relying on fragile conversation history.

### Graph state machine — nodes & edges <a id="graph-state-machine"></a>

The engine lives in `orchestrator/internal/agent`. `graph.go` holds the node set and the `graphEdges` routing table (`node → edge → nextNode`); each node lives in its own `node_*.go` file.

**The ten nodes:**

| Node | File | Responsibility |
|------|------|----------------|
| `step` | `node_step.go` | Ask the LLM for the next action; render full task state + SKILLS + DELEGATION + MEMORY into the prompt; route on the returned control tag. |
| `tool` | `node_tool.go` | Validate, gate, and execute the buffered command; detect stuck/duplicate patterns; supervise long-running commands. |
| `consult` | `node_consult.go` | Mid-command consult (e.g. long-running) — ask the LLM `[CONTINUE]`/`[ABORT]`. |
| `retry` | `retry.go` | LLM/parse-error recovery with exponential backoff; routes back to its caller or gives up. |
| `summarize` | `node_lifecycle.go` | Context compaction: when the token budget is ≥80% consumed, compress observation history into a summary. |
| `approval` | `node_lifecycle.go` | Operator approval gate (blocks the loop goroutine on a channel). |
| `finish` | `node_lifecycle.go` | Terminal: completed / failed / stopped; synthesizes a summary if the model never produced one. |
| `ask` | `node_lifecycle.go` | Terminal: pause and ask the operator a question. |
| `handoff` | `node_handoff.go` | Delegate a focused subtask to a specialist sub-agent and feed its summary back. |
| `remember` | `node_remember.go` | Persist a structured memory (e.g. a code map) to the knowledge store. |

**The walk** (`Execute` in `graph.go`):

```
            ┌──────────── budget_out / context_full ────────────┐
            ▼                                                    │
  ┌──────┐ run_command ┌──────┐  ok / is_error   ┌──────┐        │
  │ step │────────────▶│ tool │─────────────────▶│ step │        │
  └──┬───┘             └──┬───┘                  └──────┘        │
     │ finish             │ requires_approval                     │
     │ ask_operator       ▼                                       │
     │ handoff         ┌──────────┐ approved/denied               │
     │ remember        │ approval │──────────────▶ step           │
     │ update_plan     └──────────┘                               │
     │ llm_error          │ stuck / long_running                  │
     ▼                    ▼                                       │
  finish · ask        ┌─────────┐ continue/abort                  │
  (terminal)          │ consult │──────────────▶ step             │
                      └─────────┘                                 │
  llm_error ▶ retry ─(retry)▶ caller    summarize ─(done)▶ step ──┘
  handoff  ▶ handoff ─(ok)▶ step        remember  ─(ok)▶ step
```

Terminal nodes (`finish`, `ask`) have no outgoing edges — `Execute` stops when it reaches them. `retry` is special: it routes dynamically back to whichever node called it. Every transition is checkpointed, so a crashed task resumes from its last node.

### The tag protocol <a id="the-tag-protocol"></a>

The agent and the LLM speak a **closed, bracket-tagged vocabulary** (defined once in `protocol.go`'s `Registry`). This is *not* free text and *not* XML — it is `[TAG]` on its own line, optionally followed by a JSON body. The registry is the single source of truth; the prompt's protocol reference is generated from it.

**Agent → LLM (observation / envelope tags)** — how the agent hands results back:

| Tag | Meaning |
|-----|---------|
| `[TOOL_RESPONSE]` | Command succeeded; output follows. |
| `[TOOL_RESPONSE_TRUNCATED]` | Output too long; head+tail shown. Reply `[SEND_FULL]` for everything. |
| `[TOOL_RESPONSE_FULL]` | The complete output you requested via `[SEND_FULL]`. |
| `[TOOL_FAILED]` | Command ran but failed — adapt, don't repeat. |
| `[COMMAND_BLOCKED]` | Malformed, or gateway rejected the target as out of scope. |
| `[DUPLICATE_BLOCKED]` | You just ran this exact command — choose a different one. |
| `[REPEATED_FAILURE]` | Several failures in a row — switch tool/route, replan, or finish. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Repetition detected. |
| `[LONG_RUNNING]` | A command exceeded its soft limit — reply `[CONTINUE]` or `[ABORT]`. |
| `[OUTPUT_OVERFLOW]` | Terminal output is pressuring the context window. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | The operator denied / didn't answer the gated command. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plan accepted / few steps remain. |
| `[UNKNOWN]` | The agent couldn't classify the situation — you decide. |

**LLM → Agent (control / action tags)** — what the model replies with (one tag, then a JSON body):

| Tag | Body | Effect |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Run a sandbox command. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Revise the plan. |
| `[FINISH]` | `{summary, findings}` | Goal achieved. |
| `[HANDOFF]` | `{role, subgoal}` | Delegate a subtask to a specialist sub-agent. |
| `[REMEMBER]` | `{key, value}` | Persist a structured memory under a key (never forgotten). |
| `[ASK_OPERATOR]` | `{question}` | Blocked; need a human. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Resend full output / keep waiting / cancel command / stop task. |

Weak models often omit the leading tag and emit only the JSON — the parser infers the control tag from the payload shape (a `command` field → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, etc.), so the agent stays robust regardless of how poorly the model follows the format.

### Brain-agnostic core <a id="brain-agnostic-core"></a>

The LLM is an **injected, swappable dependency** behind a one-method interface (`LLM { Chat(...) }`). The core is designed for *quality and coverage*, never for the capabilities of the model currently attached. A small local model (qwen3:8b) is the development brain and a quality stress test — if the core works healthily on a small brain, it works on a large one. Plug in a stronger brain and the same core simply *expands* its level; it never has to change.

---

## The Knowledge Store — two-layer memory + learning loop <a id="the-knowledge-store"></a>

`internal/knowledge` is a **leaf package** (depends on nothing internal but `scope`) that is the **single owner of the world model**. The agent, executor, API, and all sub-agents write to one place — so the agent's discoveries become structured, queryable, shared knowledge instead of free-text trapped in one task.

**Two layers of memory:**

1. **Factual (per engagement)** — `targets` and `findings` tables. Findings carry provenance: `source` (`manual` / `agent` / `subagent`) and the `task_id` that produced them.
2. **Procedural (portable, target-scrubbed)** — the `procedural_memory` table. Every executed command is recorded as a *tactic*: `{tool, command_pattern, outcome}`, where the target token is replaced with the literal `<TARGET>` placeholder (via `scope.ExtractTarget`) so **no target-specific data leaks** into the portable layer. Identical tactics dedup with a `hit_count`. The outcome (`worked` / `failed`) is derived **mechanically** from the exit code — so this learning loop does not even depend on the brain.

**The learning loop (Hermes):**

```
run command ──▶ executor records a target-scrubbed tactic ──▶ procedural_memory
                                                                   │
next step ◀── node_step injects a MEMORY block ◀── RetrievalStrategy reads it back
```

Retrieval is behind a `RetrievalStrategy` interface. The shipped implementation, `StructuredRetrieval`, is deterministic SQL keyed on the current session and the tools of recently-executed commands — zero new dependencies, fully local. A semantic/embedding strategy can be dropped in later **without touching the core**.

**Persistent context** — the `persistent_context` table (`ON CONFLICT DO UPDATE`) holds long-lived memory that outlives a single task's sliding window: source-code connection maps, module graphs, documentation. The source-analysis sub-agent builds and updates these via `[REMEMBER]` — the "never-forget code mapper."

---

## Skills — declarative just-in-time playbooks <a id="skills"></a>

Stuffing every playbook into the prompt destroys reasoning. `internal/skill` is a **declarative registry**: each skill is a YAML file (`persona/v0.1/skills/*.yaml`) with `{name, description, triggers, tools, knowledge}`. At runtime the registry scores each skill by how many of its `triggers` appear in the goal + recently-used tools, and injects only the **top-N** relevant playbooks into the plan and step prompts — a lean, deterministic, local RAG. Skills introduce **no execution path**; they make the brain smarter about which *sandbox* commands to run. Ships with `recon`, `web`, and `source-analysis`. Add a skill by dropping a YAML file — no code change.

---

## Sub-agents — bounded delegation <a id="sub-agents"></a>

Yummi supports hierarchical delegation. When the orchestrator needs deep, specialized work, the model emits `[HANDOFF]{role, subgoal}` and the `handoff` node runs a **specialist sub-agent** — the same graph state machine, configured with its own persona and a scoped subset of skills.

- **Shared world model.** Sub-agents share the orchestrator's Store + Knowledge Store, so their findings and learned tactics join the one world model automatically. This is exactly why the Knowledge Store had to come first.
- **Token shielding.** A sub-agent runs its own loop; the parent only gets back a clean `Summary` (and the findings already in shared knowledge), not the sub-agent's internal turns.
- **Depth capped at 1.** Sub-agents are built with an *empty* sub-agent map, so they never see or accept `[HANDOFF]` — no recursion, no fork bombs.
- **Same authority.** Every sub-agent command still flows through the single exec path → gateway scope check → sandbox. A sub-agent has exactly the orchestrator's authority, no more.

Ships with `recon`, `web`, `source-analysis`, `reverser`, and `source-analyzer` roles (`persona/v0.1/subagents/*.yaml`). Add a role by dropping a YAML file.

---

## TRACE — agent observability <a id="trace-observability"></a>

The agent's intelligence is made **legible**. The graph emits typed `ActivityEvent`s — `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish` — persisted to an `agent_activity` table and streamed over the `/ws/events` WebSocket as `agent.activity`. Sub-agent events carry a `parent_task_id` so they nest under their handoff span.

- **Web AGENT tab** — an inline activity timeline: 🧠 memory recalled, 📘 skill applied, 🔎 finding, 🤝 handoff, 💾 remembered — so you see *what the agent is drawing on*, not just its text.
- **Web TRACE tab** — a full observability surface: the live activity trace (nested sub-agent spans) plus browsable Findings, learned Tactics, persistent Context (code maps), Skills, and Roles.
- **Read APIs** (read-only): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

The TUI surfaces the same activity chips live in the terminal.

---

## The conductor, validator & egress modes <a id="conductor-validator-egress"></a>

- **conductor** (`internal/conductor`) — talks to Ollama directly with a persona-driven system prompt. **There is no rule-based fallback**: if the LLM is unconfigured or unreachable, it returns an `intent: decline` with a bilingual (Turkish/English, auto-detected) explanation. Replies are structured JSON intents: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** — does **risk classification only**, no network-scope decisions. Risk comes from the model's own `risk_level` plus a conservative structural floor (`classifyRisk` only escalates to `critical` on shell-chaining operators). There is **no hardcoded tool allowlist** — to teach the agent a new tool, edit `persona/v0.1/` knowledge, not a registry.
- **Egress mode** (operator-set, gateway-enforced) — a runtime 3-way master switch above the per-IP scope list:
  - `block` (default, fail-closed) — no external egress.
  - `allow` — sandbox egress is blanket-accepted (hostnames work without per-IP resolution).
  - `ask` — each new external destination needs operator approval.
  - **The mode does not persist across gateway restarts — it resets to `block` (fail-closed).**

---

## Gateway (Rust trust boundary) <a id="gateway"></a>

The memory-safe enforcement layer (`gateway/`, binary `yummi-gateway`):

- **`policy/`** — the `PolicyEngine` (`RwLock<HashMap>` with TTL expiry); Block beats Allow; egress mode resolves a `verdict` (`allow` / `block` / `needs_approval`).
- **`network/`** — Linux/nftables-only controller managing the FORWARD chain (needs `NET_ADMIN`). Optional fingerprint masking: `ttl_normalize` (defeat passive OS fingerprinting) and `mac_randomization`.
- **`ipc/`** — Unix domain socket, length-prefixed JSON framing (`[4-byte BE length][JSON]`). Requests: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** — SQLite WAL log, each row **SHA-256-hash-chained** to the previous one (tamper-evident). Rule add/remove and egress-mode changes are recorded.
- **`watchdog.rs`** — RSS limit + heartbeat freshness; a breach triggers emergency lockdown and daemon exit.

A web GATEWAY tab exposes status, active rules, the audit log, and the egress-mode switch.

---

## Sandbox <a id="sandbox"></a>

A hardened Kali container: read-only root filesystem, runs as `1000:1000`, only `NET_RAW`/`NET_ADMIN`, `no-new-privileges`. DNS is forced through the gateway so all lookups are logged. The entrypoint just waits for the gateway's socket then `sleep infinity` — every command is a fresh `docker exec`, never a persistent shell.

---

## Interfaces — Web UI & TUI <a id="interfaces"></a>

Two front-ends, the **same REST/WS API** — neither opens a new execution path:

- **Web UI** (`ui/`, React 18 + Vite, nginx on `:3000`) — tabs: AGENT (chat + live activity timeline), BASH (interactive sandbox terminal over WS), TRACE (observability + world model), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) — a simple-clean terminal operator console: chat with proposal → approval, agent-task tracking, and the same live activity timeline (skill/memory/finding/handoff chips) over the WebSocket. Launch with `yummi --tui` or `make start-tui`.

---

## Quick start <a id="quick-start"></a>

```bash
make install   # bootstrap dev environment (Go, Rust, Node, Docker, Ollama)
make build     # build CLI + orchestrator + gateway binaries (UI built separately)
make start     # ensure Ollama is running, then docker compose up the stack
# Web dashboard → http://localhost:3000

make start-tui # start the stack and launch the terminal console instead
./bin/yummi --tui   # (if services are already up)
```

The first `make start` pulls the default model (`qwen3:8b`, ~4 GB).

---

## Developer workflow <a id="developer-workflow"></a>

```bash
make build      # CLI + orchestrator + gateway (NOT the UI — build that separately)
make test       # all suites: CLI + orchestrator + gateway (cargo) + UI (vitest)
make lint       # golangci-lint at repo root and in the orchestrator module

# Granular build
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Granular test
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # real qwen3:8b + live sandbox e2e (self-skips if sandbox is down)

# Lifecycle
make dev · dev-ui · dev-orchestrator     # hot-reload dev servers
make force-restart · close               # restart / stop everything + free VRAM
```

Two Go modules: the **root** (`./go.mod`) is the CLI + TUI; the **orchestrator** (`orchestrator/go.mod`) is everything AI/API/execution. Tests under `orchestrator/internal/*` must run from `orchestrator/` (a separate module). The gateway is Rust; the UI is vitest. **No mocks of Yummi's own components** — tests exercise real stores/engines, with only the LLM boundary stubbed.

---

## Configuration <a id="configuration"></a>

Orchestrator environment variables:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # empty = conductor declines everything (no fallback)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Tier presets resolve a model + context window + thinking mode: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (dev default) → qwen3:8b.

**Network scope is not an orchestrator setting.** It is configured exclusively in the gateway's own YAML (`scope.allow` / `scope.block` in `gateway/config/default.yaml`) — the gateway is the sole scope authority.

---

## Repository layout <a id="repository-layout"></a>

```text
cmd/yummi/              CLI entrypoint (binary: yummi)
internal/
  cli/                  terminal launch, --tui wiring, color output
  tui/                  Charm Bubble Tea terminal console (api · ws · app · render · styles)
  config/ · version/    config loader · build metadata
orchestrator/           separate Go module (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     server binary (wires every package)
  internal/
    agent/              graph state machine — graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          world model — targets · findings · procedural · context · retrieval
    skill/              declarative skill registry
    conductor/          LLM engine · validator · approval · tier · persona
    executor/ execution/  single exec path · execution store
    api/                REST routes + handlers + WS bridge
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   integration leaves
gateway/                Rust trust boundary (policy · ipc · network · audit · watchdog)
sandbox/                hardened Kali container image + entrypoint
ui/                     React 18 + Vite dashboard (served by nginx on :3000)
persona/v0.1/           document-driven persona, skills/, subagents/
```

---

## Security invariants <a id="security-invariants"></a>

If you change Yummi, preserve these:

1. **Fail-closed by default.** Scope/nftables changes must keep "deny until explicitly allowed." The gateway installs DROP rules before opening its socket.
2. **One exec path.** Pentest commands reach a shell only through `sandbox.Bridge.Exec`. Never introduce a host-side command path reachable from agent/LLM output. (`internal/mcp` spawns a host process and is therefore kept dormant — unimported by production code.)
3. **The gateway is the sole scope authority.** The orchestrator asks; it never decides reachability itself.
4. **Egress mode resets to `block` on gateway restart.**
5. **Conductor/executor separation.** The conductor knows the LLM, personas, risk — never how commands run. The executor knows gateway/sandbox/execution — never chat or proposals.

---

<div align="center">

Built with Go · Rust · React · Charm · Ollama. Conventional Commits.

</div>
