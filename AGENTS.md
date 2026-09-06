# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

Yummi is a privacy-first AI pentest conductor that translates natural language into practical security workflows while maintaining explicit traffic control and operator authority. The system enforces a fail-closed trust boundary between the AI orchestrator and the Kali-based execution sandbox.

Four components, three Cargo/Go/npm build units:

```
User/CLI  →  orchestrator (Go)  →  gateway (Rust, IPC)  →  sandbox (Kali Docker)
              REST+WS API           fail-closed nftables     tool execution only
              conductor + agent     policy engine, audit
```

- **orchestrator** — Go service; owns the AI conductor, the agentic task loop, the execution store, and the REST/WebSocket API. Never touches nftables directly.
- **gateway** — Rust daemon (binary name `yummi-gateway`); the only component that can open external traffic from sandbox. Installs DROP-all nftables rules on startup; if it crashes, those rules persist (fail-closed).
- **sandbox** — hardened Kali container (writable fs, agent commands run non-root as UID 1000, `NET_RAW`/`NET_ADMIN` + minimal filesystem-prep caps). One unified environment: the agent's `docker exec` tool runs, the operator's Bash (tmux) tab, and the graphical Desktop (noVNC/XFCE) tab all share it. Runs an always-on desktop process tree; the agent still reaches it only via `docker exec`. (Was two containers + a read-only headless box — unified 2026-09-04.)
- **ui** — React 18 + Vite SPA, served by nginx on :3000 in Docker, proxying `/api/` and `/ws/` to orchestrator on :9080. Display language (English/Turkish) is fixed at build time via `make conf`'s `YUMMI_LANG` — see [UI i18n](#code-patterns--conventions) below. This is the **only** operator surface — a native Tauri desktop shell and a terminal UI both existed at one point (`desktop/`, `internal/tui`) and were deliberately removed in favor of a single, simpler web-only surface; don't reintroduce either without a real reason. The root-module `yummi` binary (`internal/cli`) still exists but only handles Docker lifecycle/diagnostics and opens this same web UI in an app-style browser window by default — it is not a second client of the API.

## Build & Development Commands

```bash
make conf      # interactive one-time setup: pick CONTAINER_ENGINE (docker|podman),
               # YUMMI_NETWORK_MODE (secure|insecure-bridge), and YUMMI_LANG (en|tr — UI
               # build-time display language), persists to gitignored .yummi.conf so later
               # commands never need those flags again (make start CONTAINER_ENGINE=... still overrides)
make install   # bootstrap dev environment (scripts/install.sh)
make build     # build CLI + orchestrator + gateway binaries AND the UI (native dist, via vite build)
make start     # ensure Ollama is running, then $(CONTAINER_ENGINE) compose up gateway sandbox orchestrator yummi-ui docker-proxy
               # (always rebuilds images first — source changes are never silently skipped).
               # UI is served at http://localhost:3000 — Yummi is web-managed only.
make app       # alias for `make start` — kept for anyone with the old two-step habit memorized
make stop      # docker compose stop (same set)
make clean     # remove ./bin, ui/dist, and any *.db files
make test      # _test-cli + _test-orchestrator + _test-gateway + _test-ui
make lint      # golangci-lint run, twice: once at repo root, once `cd orchestrator`
make vuln      # govulncheck supply-chain scan, both Go modules
make doctor    # ./bin/yummi --doctor, falling back to scripts/doctor.sh
make dev       # scripts/dev.sh hot-reload bootstrap
make dev-ui          # Vite HMR dev server on :3000 (cd ui && npm run dev)
make dev-orchestrator # orchestrator locally on :9080, no Docker (cd orchestrator && go run ./cmd/orchestrator)
make force-restart   # docker compose down, kill ollama + yummi processes, restart
make close           # stop everything + stop the ollama service (frees RAM/VRAM)
```

The orchestrator does not embed the UI into its binary — they are separate deployables. `make build`'s UI output (`ui/dist`) is only consumed by `make docker-ui` (nginx image) or manual serving; running `make _build-ui` directly still works if you only need that piece.

### Granular build/test targets

```bash
make _build-cli          # ./bin/yummi
make _build-orchestrator # ./bin/yummi-orchestrator
make _build-gateway      # gateway/target/release/yummi-gateway
make _build-ui           # ui/dist via vite build
make docker-build        # all four service images
make docker-orchestrator / make docker-ui   # single-image rebuilds

make _test-cli           # go test ./cmd/... ./internal/...  (root module)
make _test-orchestrator  # cd orchestrator && go test ./...
make _test-gateway       # cd gateway && cargo test
make _test-ui            # cd ui && npm test (vitest)
make test-agent-live     # real qwen3:8b + live sandbox container e2e (self-skips if sandbox isn't running; start it with `make start` first)
make test-agent-eval     # assertion-bearing multi-scenario quality gate (agent/evaluation.go's QualitySpec/QualityReport)
                          # against the real model + sandbox — not print-only, fails the build on a threshold miss
```

### Running a single test

```bash
# orchestrator module — must cd or prefix, it's a separate go.mod
cd orchestrator && go test ./internal/agent -run TestRunnerHappyPath
cd orchestrator && go test ./internal/conductor -run TestValidatorToolAllowlist
cd orchestrator && go test -race ./...

# root module
go test ./internal/cli -run TestDiskFree

# gateway (Rust)
cd gateway && cargo test policy_engine::tests::test_allow_rule
```

### Running the CLI

```bash
./bin/yummi            # launches UI (default, no subcommand)
./bin/yummi --start    # start services
./bin/yummi --stop     # stop services
./bin/yummi --status   # check status
./bin/yummi --doctor   # diagnostics
./bin/yummi --version  # version info
```

## Architecture

### Three separate Go modules — don't mix them up

1. **Root module** (`./go.mod`, `github.com/n0connect/yummi`): CLI entrypoint only.
   - `cmd/yummi` — binary
   - `internal/cli` — terminal launch, flag handling, color output
   - `internal/config` — `~/.yummi/config.yaml` loader
   - `internal/version` — build metadata (ldflags-injected semver/SHA)

2. **orchestrator module** (`orchestrator/go.mod`, `github.com/n0connect/yummi/orchestrator`): everything AI/API/execution-related.
   - `cmd/orchestrator` — server binary, wires all packages
   - `internal/agent` — agentic task engine, structured as an explicit **graph state machine** (not a flat loop). `graph.go` holds the node set (`step`/`tool`/`consult`/`retry`/`summarize`/`approval`/`finish`/`ask`/`handoff`/`remember`/`threat_hold`) and the `graphEdges` routing table (node → edge → nextNode); each node lives in `node_*.go` (`node_step.go`, `node_tool.go`, `node_consult.go`, `node_lifecycle.go`, `node_handoff.go`, `node_remember.go`, `node_threat.go`). `agent.go` defines the `Agent` interface (supports sub-agent `Handoffs()`); `runner.go` implements it. Supporting files: `store.go` (SQLite task persistence + pub/sub, incl. crash-recovery of interrupted tasks), `task.go` (Task struct + status), `gate.go` (blocking approval channel), `exec.go` (enqueues into `execution.Store` — the agent **never** calls `exec.Command` itself), `prompts.go`/`protocol.go` (LLM prompt templates + tag-protocol action parser), `memory.go`, `retry.go`, `verify.go`, `delegation.go` (`DelegationCoordinator` — bounds sub-agent concurrency, tracks addressable/interruptible runs), `evaluation.go` (`QualitySpec`/`QualityReport` — assertion-bearing task-quality gate, backs `make test-agent-eval`). **This is a deliberately-designed graph flow — change it carefully and keep `make test` green; don't restructure the node/edge model casually.**
   - `internal/api` — REST routes (`routes.go`), handlers split by domain (`handlers_session.go`, `handlers_execution.go`, `handlers_system.go`, `handlers_chat.go`, `handlers_skills.go`, `handlers_trace.go`, `tasks.go`), WS terminal bridge (`terminal.go`), SSE log endpoint (`system.go`)
   - `internal/conductor` — AI engine: `engine.go` (LLM chat + persona), `validator.go` (scope + risk classification), `approval.go` (TTL proposal store), `tier.go` (Ollama tier presets), `persona.go` (YAML loader), `golden.go` (assertion-bearing persona regression fixtures)
   - `internal/execution` — command execution lifecycle store
   - `internal/executor` — bridges `execution.Store` → gateway scope check → sandbox exec
   - `internal/scope` — single source of truth for `ExtractTarget(command)`: the host/IP a tool command targets. Imported by the conductor, executor, and API so the validator and the executor always scope-check the *same* token (leaf package, no deps on either side — keeps the conductor/executor boundary clean).
   - `internal/knowledge` — two-layer memory store (`persistent_context` + `procedural_memory`/tactics), `StructuredRetrieval` and `SemanticRetrieval` (local-embedding cosine similarity via Ollama, degrades to structured when no embedder), origin/provenance tracking
   - `internal/skill` — declarative YAML playbooks, explicitly knowledge-only (no execution path); `Select`/`Subset`/`Render`; versioned (`SchemaVersion`/`Version`/`Digest`/`Provenance`), threat-scanned on load
   - `internal/skillcurator` — auto-skill-creation: drafts a candidate skill from a successful task trajectory, sanitizes it (PII/credential/target scrub, `sanitize.go`), threat-scans it, and quarantines it in a `pending_skills` store for explicit operator approval; `worker.go` runs curator LLM calls through a bounded, shutdown-aware queue so it never blocks task completion
   - `internal/security/threatscan` — content-layer prompt-injection/exfiltration/invisible-unicode scanner (scopes: all/context/strict), ANSI/C1 stripping
   - `internal/security/invariants` — executable architectural fitness-function tests (AST-walking `go/parser`) that lock the single-exec-path and mcp-dormancy invariants in CI, not just in this file's prose
   - `internal/llm` — Ollama HTTP client
   - `internal/sandbox` — Docker exec bridge to the Kali container
   - `internal/gateway` — IPC client to the gateway daemon (Unix socket, length-prefixed JSON)
   - `internal/session` — SQLite CRUD for sessions/targets/findings/audit
   - `internal/health` — ring-buffer health metrics + component pinger
   - `internal/mcp` — JSON-RPC 2.0 stdio client for MCP servers
   - `internal/dnsresolve` — leaf package dialing Cloudflare DNS (1.1.1.1:53) directly, so the orchestrator and sandbox agree on a hostname's IP before the gateway opens a rule
   - `internal/tlsutil` — mTLS cert generation (ECDSA P-256)
   - `internal/sandboxmcp` — orchestrator-side client for the sandbox-resident MCP server (module #3), reached via `docker exec -i`; used by the Eino agent backend's cataloged pentest tools

3. **sandbox-mcp module** (`sandbox-mcp/go.mod`, `github.com/n0connect/yummi/sandbox-mcp`): the MCP server that runs *inside* the Kali sandbox container — compiled into `sandbox/Dockerfile`'s image via a Go multi-stage build, and never linked against `orchestrator/` (a structural guarantee — it's simply not in this module's dependency graph). Built on the official `modelcontextprotocol/go-sdk`. Registers the cataloged pentest tools (`nmap_scan`, `gobuster_dir`, `nikto_scan`, …); each handler execs the real binary argv-style (`exec.Command("nmap", args...)`), never a shell. `make _build-sandbox-mcp`/`make _test-sandbox-mcp` for host-side dev; the Docker image recompiles it in its own build stage.

Tests under `orchestrator/internal/*` and `sandbox-mcp/` must run from their own module directory (or with that prefix) — running `go test ./...` from the repo root will silently skip them since they're different modules.

### Conductor: LLM is required, there is no rule-based fallback

`conductor.Engine` (orchestrator/internal/conductor/engine.go) talks to Ollama directly. **There is no rule-based intent engine anymore** — if `YUMMI_OLLAMA_URL` is empty, or the LLM call fails, `Chat()`/`ChatWithHistory()` return `intent: "decline"` with a bilingual (Turkish/English, auto-detected via `likelyTurkish()`) explanation. Don't assume a pattern-matching fallback exists when reading or modifying this package.

- Persona YAML in `persona/v0.1/` is loaded once at startup (`persona.go`); a load failure logs a warning and continues with an empty persona rather than failing engine construction.
- Per-session chat history is capped at `maxHistoryPerSession = 20` turns, kept in an in-memory map keyed by session ID.
- `tier.go` resolves `YUMMI_OLLAMA_TIER` (lite/pro/expert/standard) to a model + context window + thinking-mode preset; `YUMMI_OLLAMA_MODEL` is overridden by tier when both are set.

### Validator: no hardcoded tool allowlist

`validator.go` does **not** maintain a fixed list of permitted tools (e.g. there's no `isToolAllowed` or `toolregistry` package — both were removed). Risk is now derived from:

1. The model's own `risk_level` field in its structured response — primary source of truth.
2. `classifyRisk(cmd)` — a conservative structural floor that only escalates to `critical` when the command string contains shell-chaining operators (`&&`, `||`), regardless of which tool is invoked.

The validator does **no** network-scope decisions anymore — it does risk classification only. All IP allow/block logic was removed (no more `netip` checks, no `PENTEST_SCOPE_*`/`YUMMI_SCOPE_*` env reads). Scope is enforced solely by the gateway at execution time, using the gateway's own YAML config (`scope.allow`/`scope.block`); the gateway is the sole scope authority. See `docs/superpowers/specs/2026-06-22-scope-centralization-design.md`. (Declining "scan the entire internet" remains a persona rules-of-engagement judgment, unrelated to the removed CIDR checks.)

`requiresApproval()` gates on risk level alone (medium/high/critical), not on tool identity. If you're asked to "add a tool to the allowlist," there's no registry to edit and no `tool_knowledge.yaml` file — the model relies on its own pretrained tool knowledge (`persona/v0.1/agent.yaml`'s identity prose is explicit: "you are given no catalogue and need none"). For a niche tool the model doesn't already know well, add a short usage note to `core_identity.yaml` or `agent.yaml`; special-case `classifyRisk` in `validator.go` only if the tool family needs a structural risk floor.

### Key data flows

**Chat → single command:**
```
UI → POST /api/v1/chat → conductor.Engine.ChatWithHistory()
  → ChatResponse{intent:"suggest_command", proposals:[...]}
  → UI shows proposal bar → POST /api/v1/chat/approve
  → execution.Store.Enqueue() → executor.ProcessFunc → gateway.CheckScope → sandbox.Bridge.Exec
  → WS /ws/events fires execution.status events
```

**Chat → agentic task:**
```
UI → POST /api/v1/chat → conductor detects start_task intent
  → agent.Runner.CreateAndPlan() (LLM produces numbered plan)
  → agent.Runner.Start() → r.loop() → graph.Execute(state, nodeStep)
  → state machine walks nodes via graphEdges until a terminal node (finish/ask)
  → WS /ws/events fires task.status events; completion summary returned as chat reply
```
`graph.Execute` drives the node walk: `step` asks the LLM for the next action, routes to `tool` (run a command), `consult`/`retry` (recover), `approval` (risk gate), `summarize` (context compaction), or the terminal `finish`/`ask`. Each `step` re-renders full task state (plan, observations, findings) into the prompt — it never relies on conversation history, to avoid context loss on small models like qwen3:8b.

**Approval flow** (medium+ risk, in both the single-command and agent-loop paths):
```
risk ≥ medium → gate.Request() blocks the loop goroutine
  → task/proposal status → awaiting_approval
  → WS pushes pending_command + pending_risk
  → operator: POST /api/v1/tasks/{id}/approve (agent) or /api/v1/chat/approve (single command)
  → gate.Resolve(verdict) → loop resumes
```
The gate is a plain Go channel; proposals separately expire via a 20-minute TTL purger in `ApprovalStore`.

**Gateway management API/UI:** the React UI has a GATEWAY tab (between LOGS and HEALTH) showing gateway status, active rules, and the audit log, and letting operators add/remove temporary rules. Backed by orchestrator REST endpoints: `GET /api/v1/gateway/rules`, `POST /api/v1/gateway/rules` (body `{network, ttl_secs, reason}`), `DELETE /api/v1/gateway/rules/{id}`, `GET /api/v1/gateway/audit?limit=N`, `GET /api/v1/gateway/fingerprint`. A `gateway.status` event type is broadcast over the existing `/ws/events` WebSocket. Gateway's `PolicyRule` carries a `source: config|agent|manual` tag, and rule add/remove events are recorded in gateway's tamper-evident SHA-256 audit chain (previously it logged only network connection decisions).

**Egress mode (operator-controlled, gateway-enforced):** a runtime 3-way master switch above the per-IP scope list, set from the GATEWAY tab. Values: `block` (default, fail-closed — no external egress), `allow` (FORWARD chain blanket-accepts sandbox egress; hostnames like `google.com` work without per-IP resolution), `ask` (each new external destination needs operator approval). IPC `set_egress_mode`/`get_egress_mode`; REST `GET/POST /api/v1/gateway/egress-mode`. `CheckScope` now returns a `verdict` (`allow|block|needs_approval`) computed from the mode (a non-IP/hostname target is decided by the mode via `check_egress_unresolved`, no longer an "invalid IP" error). The orchestrator's `executor.RunScopedCommand` routes on the verdict: allow→run, block→`ErrOutOfScope`, needs_approval→`ErrEgressApprovalRequired`. **Mode does NOT persist across gateway restarts — it resets to `block` (fail-closed).** **Orthogonal to the agent approval mode:** the agent's `auto`/`ask_permission` governs whether a *command* needs operator sign-off; egress mode governs whether *external network access* is permitted. Two independent axes. See `docs/superpowers/specs/2026-06-22-gateway-egress-mode-design.md`. The interactive per-destination approval round-trip for `ask` mode is fully implemented, not a remaining piece: a `needs_approval` verdict holds the execution in `execution.StatusAwaitingEgressApproval`; `POST /api/v1/executions/{id}/resolve-egress` → `execution.Store.ResolveEgress` → `executor.UnlockEgressTarget` (opens the gateway allow rule for the resolved target) → the execution is requeued (`StatusQueued`) and retried automatically. See `orchestrator/internal/api/handlers_execution.go`'s `HandleResolveEgress` for the wiring.

### gateway (Rust trust boundary)

- **Fail-closed by default**: nftables DROP-all rules are installed before the IPC socket even opens. sandbox has zero connectivity until an explicit Allow rule lands.
- `policy/` — `PolicyEngine` (`RwLock<HashMap<RuleKey, Rule>>` with TTL expiry); Block rules always beat Allow rules. When the allow-list is **empty**, `evaluate()` decides via the operator's `policy.default_allow` config: `false` (the default, in `config/default.yaml`) → **deny-all / fail-closed** (`"default_deny"`); `true` → observe-only, allow everything not blocked (`"default_allow"`). `PolicyEngine::with_default_allow` threads the config in; the bare `new()` defaults to the secure `false`. (This config field used to be parsed but ignored — `evaluate()` hardcoded allow-all when the list was empty, a silent fail-open.)
- `ipc/` — Unix domain socket, length-prefixed JSON framing (`[4-byte BE length][JSON body]`); request types (see `gateway/src/ipc/protocol.rs` and the Go client `orchestrator/internal/gateway/client.go`): `check_scope`, `check_hostname`, `get_hostname_policy`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`/`get_egress_mode`.
- **Hostname policy** (`policy/hostname.rs`, config `scope.hostname_allow`/`scope.hostname_block`): name-level scope the gateway can't enforce at the packet layer (it sees only IPs), so the orchestrator consults `check_hostname` *before* it resolves a name. Verdict precedence: a `hostname_block` glob match → `block` (hard deny, wins even in `allow` egress mode); else a non-empty `hostname_allow` is a whitelist (no match → `block`); else → `neutral` (defer to the IP-egress flow). `*.example.com` matches the apex `example.com` AND any subdomain at any depth; a pattern with no `*` is exact. `executor.RunScopedCommand` calls `pc.CheckHostname(host)` for hostname targets and rejects a `block` with `ErrOutOfScope` before resolving. A name-level block is recorded in the audit chain (`reason: hostname_blocked`); view the patterns at `GET /api/v1/gateway/hostname-policy`. Lists are config-level (not runtime-editable).
- `network/` — **Linux/nftables only**. The cross-platform `NetworkController` trait and the macOS `mock.rs` no-op have been removed (`network/mod.rs` says so explicitly); `nftables.rs` is the single concrete controller, managing the nftables FORWARD chain and requiring `NET_ADMIN`. On macOS, `make _build-gateway` compiles the crate locally, and running it via `docker compose`/`make start` enforces correctly (Docker Desktop's Linux VM has a real `nf_tables`-capable kernel); only a bare host invocation of the compiled binary outside Docker has no kernel to enforce against.
- **Fingerprint masking** (config `network.*`, both default OFF): `ttl_normalize` (`Option<u8>`) rewrites the IP TTL of sandbox-originated forwarded packets to a fixed value to defeat simple passive/active OS fingerprinting (p0f, `nmap -O`); `mac_randomization` (bool) assigns a random locally-administered MAC to the egress interface at startup, meaningful on shared-L2 / internal engagements. L7 HTTP header masking is an explicit, documented **non-goal** — not implemented, since it would require the gateway to become an L7 proxy, conflicting with its simple auditable fail-closed design.
- `observer/` — passive `/proc/net/{tcp,udp}` reader that logs out-of-scope connections.
- `audit/` — SQLite WAL log, each row SHA-256-hash-chained to the previous one (tamper-evident).
- `watchdog.rs` — RSS memory limit + heartbeat freshness; breach triggers emergency lockdown and daemon exit.

### sandbox

Writable root filesystem (unified desktop environment), agent commands run as `1000:1000`, `NET_RAW`/`NET_ADMIN` plus the minimal `CHOWN`/`FOWNER`/`DAC_OVERRIDE`/`SETUID`/`SETGID` set the entrypoint needs for one-time filesystem prep + privilege-drop, `no-new-privileges:false`. DNS is set to **Cloudflare (1.1.1.1 / 1.0.0.1)** — the same resolver the orchestrator uses (`internal/dnsresolve`), so both agree on a name's IP before the gateway opens a rule; DNS queries still egress through the gateway (udp/53 is accepted in the FORWARD chain) but resolution is not the trust boundary, the connection to the resolved IP is. Default route is via gateway (`172.20.0.2`). Entrypoint sets up routing, then brings up the XFCE/VNC desktop and `exec`s `websockify` as the container's foreground/liveness process; agent and operator commands are a separate fresh `docker exec` each, independent of that process tree.

### Network topology

```
yummi_internal (172.20.0.0/24): sandbox .10, gateway .2 (also DNS), orchestrator .3, ui .4
yummi_external: gateway, orchestrator, ui only — sandbox is intentionally absent,
                its only path out is through gateway's nftables rules.
Host ports: 3000 → ui nginx, 9080 → orchestrator API (localhost only)
```

## Configuration (orchestrator env vars)

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1        YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db       YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_SANDBOX_EXEC_TIMEOUT=10m           # per-command ceiling; unset/invalid = sandbox.Bridge's own 10m default
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_GATEWAY_TLS=false                  YUMMI_TLS_DIR=~/.yummi/tls
YUMMI_OLLAMA_URL=http://localhost:11434  # empty = conductor declines everything, no fallback
YUMMI_OLLAMA_MODEL=qwen3:8b              # overridden by YUMMI_OLLAMA_TIER if set
YUMMI_OLLAMA_TIER=standard               # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Network scope is **not** an orchestrator env var anymore. It is configured exclusively via the gateway's own YAML config (`scope.allow`/`scope.block` in `gateway/config/default.yaml`, parsed by `gateway/src/config.rs` `ScopeConfig`) — the gateway is the sole scope authority.

Tier presets (hardcoded in `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry`, not loaded from YAML — see the note in `persona/README.md`): `lite` → qwen3:4b, `standard` (dev default) → qwen3:8b, `pro` → qwen3:14b, `expert` → qwen3:32b.

## Code Patterns & Conventions

- **Fail-closed by default**: see gateway section above — this is the single most important invariant in the codebase. Any change to scope checking or nftables rule install order needs to preserve "deny until explicitly allowed."
- **Command-execution paths (security invariant)**: pentest commands reach a shell through **exactly one** place — `sandbox.Bridge.Exec` (`internal/sandbox/bridge.go`), which runs `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`. The agent never shells out itself: `agent/exec.go` only enqueues into `execution.Store`, which `executor.ProcessFunc` drains (gateway scope check → `sandbox.Bridge.Exec`). The interactive terminal (`api/terminal.go`) is the only other agent-reachable exec, and it is also `docker exec`. The remaining `exec.Command` callers are operator-side and not agent-controllable: `internal/cli/*` (Docker lifecycle + browser launch) and `internal/mcp/client.go` (spawns an MCP server on the host — but **`internal/mcp` is currently not imported by any production code**, so this path is dormant). When touching execution code, do not introduce a host-side command path reachable from agent/LLM output.
  - *Target extraction is now unified* in `internal/scope.ExtractTarget` (was three divergent copies in `executor`, `conductor`, and `api`). It tokenizes the command, skips the tool name / flags / filesystem paths, pulls the host out of URLs and `--flag=value` forms, and validates each candidate — so the validator and the executor always scope-check the same token. If you change target parsing, change it there and extend `internal/scope/scope_test.go`.
  - *Gateway needs an IP, not a hostname — the orchestrator resolves first:* `gateway.CheckScope` and `AddTemporaryRule` both parse `IpAddr`/`IpNet`. `executor.hostNetwork` turns an IP target into its `/32` (v4) or `/128` (v6) prefix. A **hostname** target is resolved to IP(s) before the gateway call by `internal/dnsresolve` (a leaf package), which dials **Cloudflare DNS (1.1.1.1:53)** — the *same* resolver the sandbox is configured to use (docker-compose sets the sandbox's `dns:` to 1.1.1.1), so both sides agree on the address. `executor.RunScopedCommand` and `UnlockEgressTarget` resolve the hostname, check egress on a representative IP, and open an allow rule for **every** resolved IP (bounded at 8, via `resolvedNetworks`). DNS is deliberately **not** the trust boundary: resolving a name grants nothing — the actual connection to the resolved IP is still gated by nftables. Residual edge: a CDN hostname that round-robins to a brand-new IP *between* the approval-time resolution and the retry resolution can re-hold once (self-heals on re-approve); stable A-records and Cloudflare's consistent within-TTL answers make this rare.
- **Separation of conductor vs. executor**: `conductor` knows about the LLM, personas, proposals, risk levels — never about how commands actually run. `executor` knows about gateway/sandbox/execution store — never about chat or proposals. Keep changes on the correct side of this boundary so either can be swapped independently.
- **WebSocket origin validation**: orchestrator validates WS origin headers against localhost to prevent CSWSH.
- **Nil-safe degradation**: e.g. `bridge == nil` paths return a degraded status object, not a panic — preserve this pattern in new system-status code.
- **Error wrapping**: `fmt.Errorf("context: %w", err)`; API errors use RFC 7807 problem-detail JSON (`type`/`title`/`detail`).
- **Bilingual responses**: conductor chat replies auto-detect Turkish vs. English per-message via `likelyTurkish()` and pick the matching string — when adding new conductor-generated user-facing strings, provide both.
- **Testing**: table-driven tests (`validator_test.go`), in-memory SQLite (`":memory:"`) fixtures, `-race` for concurrency-sensitive packages. **No mocks of Yummi's own components** — tests exercise real stores/engines. The only "mock"-named files are environment/third-party shims required by the test runners and are *not* fakes of the system under test: `ui/src/test/setup.ts` (jsdom stubs for `scrollIntoView`/`ResizeObserver`/`EventSource`) and `ui/src/test/mocks/*` (stubs for the browser-only `xterm.js` + addons, which can't run in jsdom). Leave these; deleting them just breaks the UI suite. `make test-agent-live` and the `cmd/test_agent` · `cmd/test_engine` · `cmd/test_loop` dev harnesses drive the **real** qwen3:8b + live sandbox — also not mocks.
- **UI session isolation (`AssistantPanel.tsx`)**: any async call keyed to a conversation (`sendChat`, `analyzeOutput`, `approveProposal`) must capture `sid = sessionID` at call time and re-check `sessionIDRef.current === sid` before applying its result to `messages`/`proposals`/`activeTask` state. Without this guard, switching conversations while a request is in flight lets a stale response (or a stale WS `execution.status`/`agent.activity` event — filtered by `record.session_id !== sessionID`, no `'default'` special-case) paint into whatever session is now on screen, or — worse, in `auto` approval mode — auto-approve a proposal that belongs to a conversation the operator already left. `loadHistory` resets all session-scoped state (`proposals`, `activeTask`, `isLoading`, the auto-feedback refs) *synchronously* on switch, before the network round-trip, for the same reason. When adding a new async action in this file, follow the same pattern.
- **Execution heartbeat, not real output streaming**: `sandbox.Bridge.Exec` buffers a command's entire stdout/stderr and returns only on exit — there is no incremental streaming, by design (it's a single `docker exec`, not a persistent shell). Without something filling that gap, a slow real command (a full `nikto`, `nmap` against many hosts) sits silent for however long it runs, which is genuinely indistinguishable from a hang from the operator's side. `execution.Store.runWithHeartbeat` (`orchestrator/internal/execution/store.go`) republishes the `"execution.running"` event every `heartbeatInterval` (default 3s, per-Store field so tests can shrink it) with `Record.ElapsedSecs` set, while the blocking `ProcessFunc` call is still in flight in a background goroutine. The frontend (`AssistantPanel.tsx`'s `ExecutionMessage`) shows this as live elapsed time instead of a static "running" label. If you touch this path, preserve the property that `ElapsedSecs` is only ever set on non-terminal heartbeat events, never persisted, never on the final record.
- **UI i18n (build-time only, no runtime switch)**: every user-facing string in `ui/src` goes through `t('key')` from `ui/src/shared/i18n/strings.ts` — a dependency-free dictionary (`en`/`tr`), not a library like react-i18next. `tr` is typed as `satisfies Record<keyof typeof en, string>`, so a translation missing a key (or carrying a stray extra one) is a compile error, not a silent runtime fallback; `strings.test.ts` is a runtime backstop for the same invariant (mirrors `internal/security/invariants`' executable-fitness-function pattern). The active language is resolved **once at module load** from `import.meta.env.VITE_YUMMI_LANG` (defaults to `tr` if unset, e.g. bare `npm run dev`/`vitest` — an intentional asymmetry from the Makefile's own `YUMMI_LANG ?= en` default, so existing dev workflows don't silently flip language) — there is no in-app language toggle by design. `VITE_YUMMI_LANG` is set from `make conf`'s `YUMMI_LANG` choice, exported by the Makefile, and reaches both UI build paths: natively (`_build-ui`, `dev-ui`) and via Docker (`docker-compose.yml`'s `yummi-ui` build passes it as `ui/Dockerfile`'s `ARG VITE_YUMMI_LANG`, since a `docker build` doesn't see the host shell's environment otherwise). When adding new UI-facing text, add the key to both `en` and `tr` in `strings.ts` rather than inlining a literal string — a plain hardcoded string bypasses the whole mechanism and reintroduces exactly the TR/EN mixing this pattern exists to prevent.
- **Wire-exposure discipline on internal/diagnostic fields**: any Go struct field that can hold raw, target-controlled, or otherwise not-for-display content needs `json:"-"` — not just `omitempty` — because `omitempty` still serializes a populated value, it only hides an empty one. `agent.ActivityEvent.Detail` (`orchestrator/internal/agent/activity.go`) is the model to follow: its own doc comment states it can carry model rationale or compressed target output and is deliberately excluded from every REST/WS response, kept only for server-side diagnostics. `agent.Task.ObservationHistory` was found with the exact same stated intent ("holds full tool outputs for audit") but no `json:"-"` — currently harmless only because nothing populates it yet, but a landmine for whoever wires it up next. Now fixed to match `Detail`'s pattern. When adding a new field for server-side/audit-only data, default to `json:"-"` and only drop it if the field is genuinely meant for the UI.

## Known Blockers

- **Docker daemon access**: assumed at `/var/run/docker.sock`. Without it, `make start`/`make docker-build` fail and orchestrator system status reports Docker `unavailable`.
- **macOS gateway**: gateway is Linux/nftables-only by design (no cross-platform abstraction), but on macOS via Docker Desktop it runs as a container on Docker Desktop's Linux VM — a real Linux kernel with `nf_tables` support — so nftables enforcement works via the normal `make start`/docker-compose path. This was verified end-to-end (FORWARD-chain DROP-all + single-target Allow correctly gated a real connection). The one path that genuinely does NOT enforce anything is running the compiled gateway binary directly on the macOS host outside Docker (e.g. bare `cargo run`) — there's no Linux kernel there at all, so don't treat that specific invocation as a security boundary test.
- **⚠️ Docker Desktop VM networking (observed on Apple Silicon, multiple Docker Desktop versions)**: a container forwarding traffic between two custom Docker bridge networks — exactly gateway's role between `yummi_internal` and `yummi_external` — can have *all* of its forwarded traffic silently dropped by Docker Desktop's own vpnkit-based VM networking layer specifically when the source network has `internal: true` set, even though gateway's own nftables rules (verified correct) never see the problem. Isolated with a controlled experiment changing only that one flag; see `docs/installation.md`'s [Docker Desktop VM networking callout](docs/installation.md#docker-desktop-internal-network-bug) for the full reproduction and what was ruled out. Availability bug, not a fail-open — gateway's decisions stay correct, the traffic just never arrives. **Confirmed fixed by switching to [OrbStack](https://orbstack.dev/)** — identical code, identical `docker-compose.yml` (`internal: true` unchanged), rebuilt and run unmodified, DNS resolution and a real external scan both worked end to end. Also unaffected on native Linux Docker Engine. Updating Docker Desktop itself did not fix it.
- **⚠️ Podman on macOS (v6.0.2, `applehv`)**: the actual trust-boundary forwarding works correctly here (unlike Docker Desktop above) — verified with the same real-external-IP + interface-counter test. But two other, unrelated gaps currently block a working agent loop: (1) `aardvark-dns` refuses to forward DNS upstream for any `internal: true` network regardless of configured servers, so hostname-based commands fail to resolve inside sandbox (IP-based targets work fine); a fix exists (`--disable-dns` on the network + pointing `DOCKER_HOST` at `docker-proxy`'s static IP) but needs an out-of-band `podman network create` step docker-compose.yml can't express on its own. (2) `docker-proxy`'s `/var/run/docker.sock` mount doesn't reach a live Podman-API socket (503s on every request), so orchestrator can never `docker exec` into sandbox — unresolved. See `docs/installation.md`'s [Podman callout](docs/installation.md#podman-macos-gaps). OrbStack remains the only no-workarounds macOS recommendation.

## Roadmap: in-progress design directions

- **Semantic retrieval** (implemented): `knowledge.SemanticRetrieval` (`orchestrator/internal/knowledge/semantic_retrieval.go`) ranks procedural memory by embedding similarity via `llm.Client.Embed` against Ollama's `/api/embeddings` (local model, e.g. `nomic-embed-text` — stays privacy-first, no cloud call); vectors stored as little-endian BLOBs in the same SQLite file `knowledge.Store` already owns, ranked with an in-process cosine-similarity scan (`embeddings.go`, no `sqlite-vec` dependency — fine at this data scale). Falls back to `StructuredRetrieval` whenever there's nothing to rank semantically (no embedder, a failed embed call, or an empty nearest-neighbor lookup) — same nil-safe-degradation pattern as `Resilient`.
- **Auto-skill-creation** (implemented — `orchestrator/internal/skillcurator`): candidate skills are drafted by a background curator (`curator.go`'s `ProposeFromTask`, fired from `finishNode`) from a task's trajectory, never by the live agent loop. The trajectory is sanitized first (`sanitize.go`: IP/URL/email/secret redaction, bounded lengths, ANSI-stripped) and then scanned with the *same* `threatscan.FirstThreatMessage(..., ScopeStrict)` that `skill.LoadDir` already runs on hand-authored skill YAML. A cheap keyword-overlap check (`isDuplicate`) skips near-duplicates of an already-live or already-pending skill. Drafts land in a quarantined `pending_skills` store (origin `"auto"`), never auto-loaded into `Registry`. Only an explicit operator approval (`POST /api/v1/skills/pending/{id}/approve`, `handlers_skills.go`) promotes a draft — via an atomic claim → temp-file-write+fsync+rename → registry-upsert → commit sequence, so a failure at any step leaves the draft "failed," never falsely "approved" — into a real `persona/v0.1/skills/*.yaml` that `LoadDir` picks up. Curator LLM calls run through `skillcurator.Worker`, a bounded non-blocking queue with a per-job timeout, so a full or slow queue can never delay task completion.
- **CAI-inspired specialized sub-agents (implemented).** A roster of 6 sub-agent roles (`recon`, `web`, `osint`, `exploiter`, `source-analysis`, `reverser`) backed by 14 declarative skills, built on the depth-1 handoff + shared Knowledge Store architecture (`persona/v0.1/subagents/*.yaml`). Each role carries a `routing_hint` the delegator uses to route work. Still no dedicated Blue Team/defensive role — the roster is red-team-shaped, matching the sandbox's tool catalog. Semantic retrieval and auto-skill-creation have also landed.
- **License**: Apache License 2.0 (decided; see `LICENSE`, root README, and `gateway/Cargo.toml`'s `license` field). Chosen over MIT for the explicit patent grant + patent-retaliation clause, which matters for a security tool and for corporate contributors whose legal teams require it.

## Other entry points worth knowing about

- `docs/` (start at `docs/README.md`) is the canonical, kept-current reference set — it supersedes the per-module READMEs (`orchestrator/README.md`, `gateway/README.md`, `sandbox/README.md`, `ui/README.md`, `persona/README.md`) for anything beyond a quick orientation; those now each point back to their `docs/` counterpart rather than duplicating depth.
- `docs/architecture.md` has more detail on trust-boundary flow and the agentic loop's status state machine if you need it.

## Commit Message Convention

Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`, `build:`, `refactor:`). Do **not** add a `Co-Authored-By: Codex...` (or any AI co-author) trailer — permanently disabled per operator preference, all projects.
