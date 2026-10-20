# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

### Mandatory sandbox execution placement

- Run sandbox-MCP builds/tests through Docker. Never run `go test` directly in
  `sandbox-mcp` on the developer machine: live tests launch real tools.
- `make _test-sandbox-mcp` builds `sandbox-mcp/Dockerfile.test` and runs real
  tests in an isolated Kali container with no network or host mounts.
- Sandbox MCP startup, Chromium, command capture, and tmux execution require
  Linux Docker, UID 1000, and the installed sandbox binary. No host-browser
  discovery or environment-variable bypass is permitted.
- Web-search HTTP, DNS, provider credentials, and content parsing must execute
  inside the gateway-routed sandbox. Do not add a host/orchestrator research
  client. `web_search` and `web_fetch` execute only inside sandbox-MCP;
  provider credentials/model are sandbox configuration and never tool args.
- New tests use real components; do not introduce mocks, fake providers,
  injected resolvers, or replacement transports.

Yummi is a privacy-first AI pentest conductor that translates natural language into practical security workflows while maintaining explicit traffic control and operator authority. The system enforces a fail-closed trust boundary between the AI orchestrator and the Kali-based execution sandbox.

Five default Compose services (`orchestrator`, `gateway`, `sandbox`, `docker-proxy`, `yummi-ui`); three Go modules, one Rust crate and one npm UI package:

```
User/CLI  →  orchestrator (Go)  →  gateway (Rust, IPC)  →  sandbox (Kali Docker)
              REST+WS API           fail-closed nftables     tool execution only
              conductor + agent     policy engine, audit     (unified XFCE/noVNC + tmux)
```

- **orchestrator** — Go service; owns the AI conductor, the agentic task loop, the execution store, and the REST/WebSocket API. Never touches nftables directly.
- **gateway** — Rust daemon (binary name `yummi-gateway`); the only component that can open external traffic from sandbox. Installs DROP-all nftables rules on startup; if it crashes, the last installed kernel rules persist. A previously allowed mode is not automatically replaced with lockdown on a crash; startup resets to `block_all`.
- **sandbox** — hardened Kali container (writable fs, agent commands run non-root as UID 1000, `NET_RAW`/`NET_ADMIN` + minimal filesystem-prep caps). One unified environment: the agent's `docker exec` tool runs, the operator's Bash (tmux) tab, and the graphical Desktop (noVNC/XFCE) tab all share it. Runs an always-on desktop process tree; the agent still reaches it only via `docker exec`. (Was two containers + a read-only headless box — unified 2026-09-04.)
- **ui** — React 18 + Vite SPA, served by nginx on :3000 in Docker, proxying `/api/` and `/ws/` to orchestrator on :9080. Features eight dedicated operator tabs: `[AGENT] [TERMINALS] [TRACE] | [BASH] [DESKTOP] [LOGS] [GATEWAY] [HEALTH]`. Includes persistent `TaskSummaryMessage` completion cards with automated CTF/pentest flag extraction & copy button, on-demand click-to-connect noVNC viewer (XFCE/VNC already run in the sandbox), and multiline auto-expanding chat input with file drag & drop. Display language (English/Turkish) is fixed at build time via `make conf`'s `YUMMI_LANG` — see [UI i18n](#code-patterns--conventions) below. This is the **only** operator surface — a native Tauri desktop shell and a terminal UI both existed at one point (`desktop/`, `internal/tui`) and were deliberately removed in favor of a single, simpler web-only surface; don't reintroduce either without a real reason. The root-module `yummi` binary (`internal/cli`) still exists but only handles Docker lifecycle/diagnostics and opens this same web UI in an app-style browser window by default — it is not a second client of the API.

There is also an optional Python process, **`brain/`** — a separate Python/LangGraph process that drives the same substrate through the orchestrator's existing REST API + an MCP server, for operators who want a frontier model instead of the local-model `graph`/`eino` backends. See [brain/README.md](brain/README.md).

## Build & Development Commands

```bash
make conf      # interactive one-time setup: detect/select CONTAINER_ENGINE (docker|podman),
               # YUMMI_NETWORK_MODE (secure|insecure-bridge), YUMMI_LANG (en|tr — UI
               # build-time display language), and local/cloud/custom model setup (wizard writes eino),
               # persists to gitignored .yummi.conf so later commands never need those flags
               # again (make start CONTAINER_ENGINE=... still overrides)
make install   # bootstrap dev environment (scripts/install.sh)
make build     # legacy aggregate: also includes a host sandbox-MCP build; use granular native targets below plus Docker for sandbox-MCP
make start     # ensure configured model/runtime, then compose up all five services
               # builds missing images; existing images are reused. Use BUILD=1 after source changes.
               # UI is served at http://localhost:3000 — Yummi is web-managed only.
make app       # alias for `make start` — kept for anyone with the old two-step habit memorized
make stop      # docker compose stop (same set)
make clean     # remove native outputs, UI node_modules/dist, Brain env/checkpoints and repo-local *.db files (not Docker volumes)
make test      # CLI + orchestrator + gateway + UI + Docker sandbox-MCP + optional Brain (uv) + QC
make lint      # golangci-lint run, three times: once at repo root, once `cd orchestrator`, once `cd sandbox-mcp`
make vuln      # govulncheck supply-chain scan, all three Go modules
make doctor    # ./bin/yummi --doctor, falling back to scripts/doctor.sh
make dev       # scripts/dev.sh hot-reload bootstrap
make dev-ui          # Vite HMR dev server on :3000 (cd ui && npm run dev)
make dev-orchestrator # orchestrator locally on :9080, no Docker (cd orchestrator && go run ./cmd/orchestrator)
make force-restart   # docker compose down, kill ollama + yummi processes, restart
make close           # stop everything + stop the ollama service (frees RAM/VRAM)

make brain-install   # cd brain && uv sync — install the optional LangGraph brain's Python deps
make brain-run       # run the brain against a goal: make brain-run GOAL="..."
make brain-mcp       # run brain/'s MCP server standalone
```

The orchestrator does not embed the UI. `make _build-ui` produces `ui/dist` for manual serving; `ui/Dockerfile` builds its own bundle from source. Container images do not consume `bin/` or native `ui/dist` outputs. For native development use `make _build-cli _build-orchestrator _build-gateway _build-ui`; build/test sandbox-MCP only through Docker.

### Granular build/test targets

```bash
make _build-cli          # ./bin/yummi
make _build-orchestrator # ./bin/yummi-orchestrator
make _build-gateway      # gateway/target/release/yummi-gateway
make _build-ui           # ui/dist via vite build
make docker-build        # all five service images
make docker-orchestrator / make docker-ui   # single-image rebuilds

make _test-cli           # go test ./cmd/... ./internal/...  (root module)
make _test-orchestrator  # cd orchestrator && go test ./...
make _test-gateway       # cd gateway && cargo test
make _test-ui            # UI vitest (unsets VITE_YUMMI_LANG for default-language fixtures)
make _test-sandbox-mcp   # real installed MCP/tools in isolated Kali Docker
make _test-brain         # optional Python tests, skips when uv is absent
make _test-qc            # Python evidence-validator tests
make test-agent-live     # real qwen3:8b + live sandbox container e2e (self-skips if sandbox isn't running; start it with `make start` first)
make test-agent-eval     # assertion-bearing multi-scenario quality gate (agent/evaluation.go's QualitySpec/QualityReport)
                          # against the real model + sandbox — not print-only, fails the build on a threshold miss
```

### Running a single test

```bash
# orchestrator module — must cd or prefix, it's a separate go.mod
cd orchestrator && go test ./internal/agent -run TestRunnerE2E_AutoModeStillGatesOnDestructiveCommand
cd orchestrator && go test ./internal/conductor -run TestValidatorApprovalRequired
cd orchestrator && go test -race ./...

# root module
go test ./internal/cli -run TestParseTopLevelOptionsDefaultUI

# gateway (Rust)
cd gateway && cargo test policy::tests::test_allow_in_scope
```

### Running the CLI

```bash
./bin/yummi            # launches UI (default, no subcommand)
./bin/yummi --start    # start configured sandbox service (use make start for full stack)
./bin/yummi --stop     # stop configured sandbox service (use make stop for full stack)
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
   - `internal/agent` — agentic task engine, structured as an explicit **graph state machine** (not a flat loop). `task_runner.go` defines the `TaskRunner` interface — the full lifecycle surface (`CreateAndPlan`/`Start`/`Stop`/`Approve`/`ApprovalMode`/`SetApprovalMode`/`ResumeRecovered`/`Gate`/`Roles`/`DelegationRuns`/`InterruptDelegation`, plus `Agent`'s `Name`/`Handoffs`/`Run`) that `internal/api` depends on. `graph.go` holds the node set (`step`/`tool`/`consult`/`retry`/`summarize`/`approval`/`finish`/`ask`/`handoff`/`remember`/`skill_view`/`threat_hold`) and the `graphEdges` routing table. Supporting files: `store.go`, `task.go`, `gate.go`, `exec.go`, `prompts.go`/`protocol.go`, `memory.go`, `retry.go`, `verify.go`, `delegation.go`, `evaluation.go`.
   - `internal/agent/eino` — a second `TaskRunner` implementation on [CloudWeGo's Eino ADK](https://github.com/cloudwego/eino) for native tool-calling against larger/more capable local models (selected via `YUMMI_AGENT_BACKEND=eino`, default `graph`). Reuses the graph engine's own `Execer`/`Verify`/`threatscan`/`Gate`/`conductor.Validator` unchanged. Sandbox MCP discovery is the source of truth for tool names/schemas; it covers typed/cataloged tools, utilities, persistent tmux, browser and research tools, alongside the orchestrator's separate `run_command` fallback.
   - `internal/api` — REST routes (`routes.go`), handlers split by domain (`handlers_session.go`, `handlers_execution.go`, `handlers_gateway.go`, `handlers_system.go`, `handlers_chat.go`, `handlers_skills.go`, `handlers_trace.go`, `tasks.go`), WS terminal bridge (`terminal.go`), desktop WS bridge, SSE log endpoint (`system.go`).
   - `internal/conductor` — AI engine: `engine.go` (LLM chat + persona), `validator.go` (risk classification only), `approval.go` (TTL proposal store), `tier.go` (Ollama tier presets: standard=`gpt-oss:20b`, expert=`gpt-oss:120b`), `persona.go` (YAML loader), `golden.go` (assertion-bearing persona regression fixtures).
   - `internal/execution` — command execution lifecycle store.
   - `internal/executor` — bridges `execution.Store` → gateway scope check → sandbox exec (`RunScopedCommand`, `processMCP`).
   - `internal/scope` — single source of truth for `ExtractTargets(command)` / `ExtractTarget(command)`.
   - `internal/knowledge` — two-layer memory store (`persistent_context` + `procedural_memory`/tactics), `StructuredRetrieval` and `SemanticRetrieval` (local-embedding cosine similarity via Ollama, degrades to structured when no embedder), origin/provenance tracking.
   - `internal/skill` — declarative YAML playbooks, explicitly knowledge-only (no execution path); `Select`/`Subset`/`Render`; versioned, threat-scanned on load.
   - `internal/skillcurator` — auto-skill-creation: drafts a candidate skill from a successful task trajectory, sanitizes it (PII/credential/target scrub, `sanitize.go`), threat-scans it, and quarantines it in a `pending_skills` store for explicit operator approval.
   - `internal/security/threatscan` — content-layer prompt-injection/exfiltration/invisible-unicode scanner (scopes: all/context/strict), ANSI/C1 stripping.
   - `internal/security/invariants` — executable architectural fitness-function tests (AST-walking `go/parser`) that lock the single-exec-path and mcp-dormancy invariants in CI.
   - `internal/llm` — Ollama HTTP client (`client.go`), embeddings (`embed.go`), resilience wrapper (`resilient.go`).
   - `internal/sandbox` — Docker exec bridge to the Kali container (`bridge.go`).
   - `internal/gateway` — IPC client to the gateway daemon (Unix socket, length-prefixed JSON).
   - `internal/session` — SQLite CRUD for sessions/chat/audit.
   - `internal/retention` — background pruning of old execution/chat logs (`YUMMI_DATA_RETENTION_DAYS`).
   - `internal/health` — ring-buffer health metrics + component pinger.
   - `internal/mcp` — JSON-RPC 2.0 stdio client for MCP servers (dormant).
   - `internal/sandboxmcp` — orchestrator-side client for the sandbox-resident MCP server reached via persistent `docker exec -i`.
   - `internal/dnsresolve` — leaf package dialing Cloudflare DNS (1.1.1.1:53) directly.
   - `internal/tlsutil` — mTLS cert generation (ECDSA P-256).

3. **sandbox-mcp module** (`sandbox-mcp/go.mod`, `github.com/n0connect/yummi/sandbox-mcp`): the MCP server that runs *inside* the Kali sandbox container — compiled into `sandbox/Dockerfile`'s image via a Go multi-stage build. MCP `tools/list` registers the current catalog: typed/cataloged pentest tools, 2 pure-Go utilities (`consolidate_hosts`, `passive_recon_batch`), 7 persistent tmux tools, 9 browser tools (`browser_open`, `browser_snapshot`, `browser_interact`, `browser_eval`, `browser_screenshot`, `browser_storage`, `browser_tabs`, `browser_logs`, `browser_session`), and 2 sandbox-only research tools (`web_search`, `web_fetch`). Discover actual schemas rather than relying on historical tool counts. Persistent Docker transport must outlive individual execution contexts; close it on orchestrator shutdown. Research observations must remain bounded valid JSON, not generic head/tail fragments.

Root `go test ./...` skips the other Go modules. Run orchestrator tests from `orchestrator/`; run sandbox-MCP builds/tests only through the Docker targets above, never a host `cd sandbox-mcp && go test` invocation.

### Conductor: LLM is required, there is no rule-based fallback

`conductor.Engine` uses Ollama by default. An explicit `YUMMI_EINO_MODEL_BASE_URL` selects an OpenAI-compatible chat client for the conductor and graph planning/chat as well as Eino. Hosted endpoints send model input to that provider; they are opt-in. With neither an Ollama URL nor an override client, or on a failed call, chat declines with a Turkish/English explanation. There is no rule-based intent fallback.

- Persona YAML in `persona/v0.1/` is loaded once at startup (`persona.go`); a load failure logs a warning and continues with an empty persona rather than failing engine construction.
- Per-session chat history is capped at `maxHistoryPerSession = 20` turns, kept in an in-memory map keyed by session ID.
- `tier.go` resolves `YUMMI_OLLAMA_TIER` (standard/expert) to a model + context window + thinking-mode preset. A configured `YUMMI_OLLAMA_MODEL` overrides the tier default (`config.Load()`). Tier presets: `standard` (`gpt-oss:20b`), `expert` (`gpt-oss:120b`).

### Validator: no hardcoded tool allowlist

`validator.go` does **not** maintain a fixed list of permitted tools (e.g. there's no `isToolAllowed` or `toolregistry` package — both were removed). Risk is now derived from:

1. The model's own `risk_level` field in its structured response — primary source of truth.
2. `classifyRisk(cmd)` — simple recognized single-command shapes floor at `low`; vetted local pipelines floor at `medium`; unrecognized shell structures default to `critical`.
3. `destructiveFloor(cmd)` — recognized destructive patterns set `ForceApprovalReason` and force an approval, even in agent `auto` mode.

The validator does **no** network-scope decisions anymore — it does risk classification only. All IP allow/block logic was removed (no more `netip` checks, no `PENTEST_SCOPE_*`/`YUMMI_SCOPE_*` env reads). Scope is enforced solely by the gateway at execution time, using the gateway's own YAML config (`scope.allow`/`scope.block`); the gateway is the sole scope authority. See [Security Model](docs/security-model.md). (Declining "scan the entire internet" remains a persona rules-of-engagement judgment, unrelated to the removed CIDR checks.)

`requiresApproval()` determines proposal risk gating, not permitted tool identity. There is no `toolregistry` identity allowlist or `tool_knowledge.yaml` file to edit. Structured MCP tools have discovered schemas and an orchestrator catalog; shell commands use the installed sandbox tools. Consult current MCP schemas and the sandbox environment manifest when extending tool support. Preserve structural and destructive risk floors in `validator.go`.

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
  → WS /ws/events fires task.status events; completion summary is persisted to session chat
```
`graph.Execute` drives the node walk: `step` asks the LLM for the next action, routes to `tool` (run a command), `consult`/`retry` (recover), `approval` (risk gate), `summarize` (context compaction), or the terminal `finish`/`ask`. Each `step` re-renders full task state (plan, observations, findings) into the prompt — it never relies on conversation history, to avoid context loss on small models like qwen3:8b.

**Approval flow:** single-command proposals gate on risk; agent tasks in
`ask_permission` gate every command unless the operator's per-task override
applies. Destructive command floors always gate, including in `auto` mode.
```
agent command requires approval → gate.Request() blocks the loop goroutine
  → task/proposal status → awaiting_approval
  → WS pushes pending_command + pending_risk
  → operator: POST /api/v1/tasks/{id}/approve (agent) or /api/v1/chat/approve (single command)
  → gate.Resolve(verdict) → loop resumes
```
The gate is a plain Go channel; proposals separately expire via a 20-minute TTL purger in `ApprovalStore`.
Tasks also expose ephemeral `pending_approval_kind` (`command` or `threat`),
`pending_mcp_tool` and JSON-encoded `pending_mcp_args`. Automated operator clients
must validate exact structured arguments and proposal identity; display labels
are not executable authority. Graph MCP arguments are frozen before approval,
and approval dispatches through `MCPExec`, preserving output threat quarantine.

**Gateway management API/UI:** the React UI has a GATEWAY tab (between LOGS and HEALTH) showing gateway status, active rules, and the audit log, and letting operators add/remove temporary rules. Backed by orchestrator REST endpoints: `GET /api/v1/gateway/rules`, `POST /api/v1/gateway/rules` (body `{network, ttl_secs, reason}`), `DELETE /api/v1/gateway/rules/{id}`, `GET /api/v1/gateway/audit?limit=N`, `GET /api/v1/gateway/fingerprint`. A `gateway.status` event type is broadcast over the existing `/ws/events` WebSocket. Gateway's `PolicyRule` carries a `source: config|agent|manual` tag, and rule add/remove events are recorded in gateway's tamper-evident SHA-256 audit chain (previously it logged only network connection decisions).

**Egress mode (operator-controlled, gateway-enforced):** a runtime 4-way master switch above the per-IP scope list, set from the GATEWAY tab. Values:
- `block_all` (alias `block`, default, fail-closed — drops all gateway-forwarded sandbox egress, including established connections and DNS, ignoring active allow rules).
- `block_all_except_rules` (alias `block_except_rules`, `rules_only` — strict scope: drops outbound traffic by default, but permits destinations explicitly matched by active allow rules or configured scope).
- `allow_all` (alias `allow` — FORWARD chain blanket-accepts sandbox egress to public destinations; hostnames like `google.com` work without per-IP resolution).
- `ask_every_time` (alias `ask` — each new external destination needs interactive operator approval).

UDP/53 is a gateway-routed resolution exception in the other three modes,
after configured destination blocks. Gateway-forwarded sandbox packets recheck
current scope. In rules-only/ask mode, expiry/removal revokes established flows
that depended on that approval and have no other matching allow. `allow_all`
does not require temporary approvals. Local and same-subnet traffic, including
the read-only policy listener, is outside this gateway FORWARD-chain boundary.

IPC `set_egress_mode`/`get_egress_mode`; REST `GET/POST /api/v1/gateway/egress-mode`. `CheckScope` returns a `verdict` (`allow|block|needs_approval`) computed from the mode (a non-IP/hostname target is decided by the mode via `check_egress_unresolved`, no longer an "invalid IP" error). The orchestrator's `executor.RunScopedCommand` routes on the verdict: allow→run, block→`ErrOutOfScope`, needs_approval→`ErrEgressApprovalRequired`. **Mode does NOT persist across gateway restarts — it resets to `block_all` (fail-closed).** **Orthogonal to the agent approval mode:** the agent's `auto`/`ask_permission` governs whether a *command* needs operator sign-off; egress mode governs whether *external network access* is permitted. Two independent axes. See [Gateway documentation](docs/modules/gateway.md). The interactive per-destination approval round-trip for `ask` mode is fully implemented: a `needs_approval` verdict holds the execution in `execution.StatusAwaitingEgressApproval`; `POST /api/v1/executions/{id}/resolve-egress` → `execution.Store.ResolveEgress` → `executor.UnlockEgressTarget` (opens the gateway allow rule for the resolved target) → the execution is requeued (`StatusQueued`) and retried automatically. See `orchestrator/internal/api/handlers_execution.go`'s `HandleResolveEgress` for the wiring.

### gateway (Rust trust boundary)

- **Fail-closed by default**: nftables DROP-all rules are installed before the IPC socket opens. Startup `block_all` ignores even active allow rules; outbound access requires an explicit operator mode change, plus applicable scope/approval in restricted modes.
- `policy/` — `PolicyEngine` (`RwLock<HashMap<RuleKey, Rule>>` with TTL expiry); Blocks take precedence except a manual allow can override an equally or more broadly configured private-range block when the source-backed specificity rules permit it; agent-generated allows do not receive that override. This is policy evaluation only: the current nftables controller emits configured block DROPs ahead of dynamic allows, so a manual policy override does not bypass a configured packet block. The controller also does not inject every policy address-class floor automatically. When the allow-list is **empty**, `evaluate()` decides via the operator's `policy.default_allow` config: `false` (the default, in `config/default.yaml`) → **deny-all / fail-closed** (`"default_deny"`); `true` → observe-only, allow everything not blocked (`"default_allow"`). `PolicyEngine::with_default_allow` threads the config in; the bare `new()` defaults to the secure `false`. (This config field used to be parsed but ignored — `evaluate()` hardcoded allow-all when the list was empty, a silent fail-open.)
- `ipc/` — Unix domain socket, length-prefixed JSON framing (`[4-byte BE length][JSON body]`); request types (see `gateway/src/ipc/protocol.rs` and the Go client `orchestrator/internal/gateway/client.go`): `check_scope`, `check_hostname`, `get_hostname_policy`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`/`get_egress_mode`.
- **Hostname policy** (`policy/hostname.rs`, config `scope.hostname_allow`/`scope.hostname_block`): name-level scope the gateway can't enforce at the packet layer (it sees only IPs), so the orchestrator consults `check_hostname` *before* it resolves a name. Verdict precedence: a `hostname_block` glob match → `block` (hard deny, wins even in `allow` egress mode); else a non-empty `hostname_allow` is a whitelist (no match → `block`); else → `neutral` (defer to the IP-egress flow). `*.example.com` matches the apex `example.com` AND any subdomain at any depth; a pattern with no `*` is exact. `executor.RunScopedCommand` calls `pc.CheckHostname(host)` for hostname targets and rejects a `block` with `ErrOutOfScope` before resolving. A name-level block is recorded in the audit chain (`reason: hostname_blocked`); view the patterns at `GET /api/v1/gateway/hostname-policy`. Config provides the startup lists; operator POST/DELETE `/api/v1/gateway/hostname-policy` can add/remove runtime patterns. Sandbox policy reads cannot mutate them.
- `network/` — **Linux/nftables only**. The cross-platform `NetworkController` trait and the macOS `mock.rs` no-op have been removed (`network/mod.rs` says so explicitly); `nftables.rs` is the single concrete controller, managing the nftables FORWARD chain and requiring `NET_ADMIN`. On macOS, `make _build-gateway` compiles the crate locally, and running it via `docker compose`/`make start` enforces correctly (Docker Desktop's Linux VM has a real `nf_tables`-capable kernel); only a bare host invocation of the compiled binary outside Docker has no kernel to enforce against.
- **Fingerprint masking** (config `network.*`, both default OFF): `ttl_normalize` (`Option<u8>`) rewrites the IP TTL of sandbox-originated forwarded packets to a fixed value to reduce one passive/active OS fingerprint signal (it does not defeat all p0f or `nmap -O` signals); `mac_randomization` (bool) assigns a random locally-administered MAC to the egress interface at startup, meaningful on shared-L2 / internal engagements. L7 HTTP header masking is an explicit, documented **non-goal** — not implemented, since it would require the gateway to become an L7 proxy, conflicting with its simple auditable fail-closed design.
- `observer/` — passive `/proc/net/{tcp,udp}` reader that logs out-of-scope connections.
- `audit/` — SQLite WAL log, each row SHA-256-hash-chained to the previous one (tamper-evident).
- `watchdog.rs` — RSS memory limit + heartbeat freshness; breach triggers emergency lockdown and daemon exit.

### sandbox

Writable root filesystem (unified desktop environment), container hostname `yummi-pwnbox`, agent commands run as `1000:1000`, `NET_RAW`/`NET_ADMIN` plus the minimal `CHOWN`/`FOWNER`/`DAC_OVERRIDE`/`SETUID`/`SETGID` set the entrypoint needs for one-time filesystem prep + privilege-drop, `no-new-privileges:false`. DNS is set to **Cloudflare (1.1.1.1 / 1.0.0.1)** — the same resolver the orchestrator uses (`internal/dnsresolve`), to reduce resolution differences; independent queries can still return different answers; DNS queries still egress through the gateway (UDP/53 is an explicit exception in non-lockdown modes, after destination blocks; `block_all` denies DNS too) but resolution is not the trust boundary, the connection to the resolved IP is. Default route is via gateway (`172.20.0.2`). Entrypoint sets up routing and attempts `/dev/net/tun` creation before the privilege drop. `openvpn` is installed, but creating a missing device node requires `MKNOD`, absent from the checked-in Compose capability set; VPN readiness is not guaranteed. It then brings up the XFCE/VNC desktop and `exec`s root-owned `websockify` as the foreground/liveness child of Docker's injected init; agent and operator commands are a separate fresh `docker exec` each, independent of that process tree. Full offensive operations arsenal: `metasploit-framework` (`msfconsole`, `msfvenom`), `nishang` (PowerShell offensive framework), `kerbrute` (rapid Kerberos enum/spray); `krb5-user` (`kinit`/`klist`/`kdestroy`) for native Kerberos ticket management; `revshell` CLI (`/usr/local/bin/revshell [PORT]`) that auto-detects `tun0`/`eth0` and prints copy-paste payloads for 8+ shell flavors; wordlist shortcut variables (`$ROCKYOU`, `$COMMON_TXT`, `$BIG_TXT`, `$MEDIUM_TXT`, `$USERNAMES`, `$PASSWORDS_TOP`) exported from `.bashrc` for both operator and agent.


### Network topology

```
yummi_internal (172.20.0.0/24, internal): sandbox .10, gateway .2, orchestrator .3
  Sandbox policy reads: 172.20.0.3:9081, no operator or rule-mutation routes.
yummi_control (172.21.0.0/24, internal): orchestrator .3, UI .4, Docker broker .5
  Sandbox and gateway are absent. Broker permits only the configured sandbox,
  UID/GID 1000:1000 and broker-issued exec IDs; no general Docker API.
yummi_ingress (172.22.0.0/24, normal bridge): UI .4 only, bridge gateway .1
yummi_external: gateway and orchestrator only; sandbox egress routes via gateway.
Host loopback ports 3000 and 9080 → UI nginx :80 → orchestrator :9080.
The orchestrator has no published port.
```

The main API still binds `0.0.0.0:9080` inside its container, but
`OperatorAccess` accepts only exact configured peers (Compose: loopback and
UI `172.21.0.4/32`), using the socket peer rather than forwarded headers.
The separate policy listener exposes only hostname-policy and literal-IP egress
checks. nginx accepts the configured loopback/bridge-gateway peers and local
Host names, then proxies over control. Isolated OrbStack checks observed host
requests arriving as exactly `172.22.0.1`; do not widen this to a subnet.

## Configuration (orchestrator env vars)

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1        YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db       YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_SANDBOX_DESKTOP_ADDR=sandbox:6080  # host:port of websockify desktop stream
YUMMI_SANDBOX_EXEC_TIMEOUT=10m           # per-command ceiling; unset/invalid = sandbox.Bridge's own 10m default
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_GATEWAY_TLS=false                  YUMMI_TLS_DIR=~/.yummi/tls
YUMMI_OLLAMA_URL=http://localhost:11434  # empty + no endpoint override = no model backend
YUMMI_OLLAMA_MODEL=gpt-oss:20b           # explicit model wins; tier selects model only when model is unset
YUMMI_OLLAMA_TIER=standard               # standard | expert
YUMMI_AGENT_BACKEND=graph                # graph (graph state machine) | eino (ReAct loop)
YUMMI_EINO_PROVIDER=ollama               # ollama | openrouter | openai
YUMMI_EINO_MODEL_BASE_URL=               # custom OpenAI-compatible / OpenRouter base URL
YUMMI_EINO_MODEL_NAME=                   # custom model tag (e.g., anthropic/claude-3.5-sonnet, gpt-4o)
YUMMI_EINO_MODEL_API_KEY=                # API key for OpenRouter / OpenAI
YUMMI_DATA_RETENTION_DAYS=0              # 0 = retain indefinitely, >0 = auto-prune aged sessions
YUMMI_PERSONA_DIR=persona/v0.1
```

For native defaults versus container overrides and exported settings, see [Configuration](docs/configuration.md).

Network scope is **not** an orchestrator env var anymore. It is configured exclusively via the gateway's own YAML config (`scope.allow`/`scope.block` in `gateway/config/default.yaml`, parsed by `gateway/src/config.rs` `ScopeConfig`) — the gateway is the sole scope authority.

Tier presets (hardcoded in `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry`, not loaded from YAML — see the note in `persona/README.md`): `standard` (dev default) → `gpt-oss:20b`, `expert` → `gpt-oss:120b`.

## Code Patterns & Conventions

- **Fail-closed by default**: see gateway section above — this is the single most important invariant in the codebase. Any change to scope checking or nftables rule install order needs to preserve "deny until explicitly allowed."
- **Command-execution paths (security invariant)**: pentest commands reach a shell through **exactly one** place — `sandbox.Bridge.Exec` (`internal/sandbox/bridge.go`), which runs `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>` — **plus one deliberate, narrower second path** for cataloged pentest tools and terminal sessions via the sandbox-resident MCP server: `internal/sandboxmcp.Client` runs a persistent `docker exec -i --user 1000:1000 <container> /usr/local/bin/yummi-sandbox-mcp`. The agent never shells out itself: `agent/exec.go` only enqueues into `execution.Store`, which `executor.ProcessFunc` drains (gateway scope check → `sandbox.Bridge.Exec` or `processMCP` → `sandboxmcp.Client.CallTool`). The interactive terminal (`api/terminal.go`) is the only other agent-reachable exec, and it is also `docker exec`. The remaining `exec.Command` callers are operator-side and not agent-controllable: `internal/cli/*` (Docker lifecycle + browser launch) and `internal/mcp/client.go` (spawns an MCP server on the host — but **`internal/mcp` is currently not imported by any production code**, so this path is dormant). When touching execution code, do not introduce a host-side command path reachable from agent/LLM output.
  - *Target extraction is now unified* in `internal/scope.ExtractTarget` (was three divergent copies in `executor`, `conductor`, and `api`). It tokenizes the command, skips the tool name / flags / filesystem paths, pulls the host out of URLs and `--flag=value` forms, and validates each candidate — so execution callers share parsing; the validator itself does not scope-check. If you change target parsing, change it there and extend `internal/scope/scope_test.go`.
  - *Target resolution and egress approval:* shell targets come from shared `scope.ExtractTargets`. The executor checks hostname policy before resolving names through `internal/dnsresolve`, then checks every extracted destination before opening permitted temporary rules. Network rules use literal IP prefixes. Gateway unresolved-target checks are mode-driven. Research DNS/HTTP is a separate sandbox-only client and returns pinned addresses through its typed execution approval/retry path. DNS does not grant permission, and independent resolver answers can differ; see [Sandbox Execution](docs/agent/sandbox-execution.md) and [Security Model](docs/security-model.md).
- **Separation of conductor vs. executor**: `conductor` knows about the LLM, personas, proposals, risk levels — never about how commands actually run. `executor` knows about gateway/sandbox/execution store — never about chat or proposals. Keep changes on the correct side of this boundary so either can be swapped independently.
- **WebSocket origin validation**: orchestrator validates WS origin headers against localhost to prevent CSWSH.
- **Nil-safe degradation**: e.g. `bridge == nil` paths return a degraded status object, not a panic — preserve this pattern in new system-status code.
- **Error wrapping**: `fmt.Errorf("context: %w", err)`; API errors use RFC 7807 problem-detail JSON (`type`/`title`/`detail`).
- **Bilingual responses**: conductor chat replies auto-detect Turkish vs. English per-message via `likelyTurkish()` and pick the matching string — when adding new conductor-generated user-facing strings, provide both.
- **Testing**: table-driven tests (`validator_test.go`), in-memory SQLite (`":memory:"`) fixtures, `-race` for concurrency-sensitive packages. **No mocks of Yummi's own components** — tests exercise real stores/engines. The only "mock"-named files are environment/third-party shims required by the test runners and are *not* fakes of the system under test: `ui/src/test/setup.ts` (jsdom stubs for `scrollIntoView`/`ResizeObserver`/`EventSource`) and `ui/src/test/mocks/*` (stubs for the browser-only `xterm.js` + addons, which can't run in jsdom). Leave these; deleting them just breaks the UI suite. `make test-agent-live` and the `cmd/test_agent` · `cmd/test_engine` · `cmd/test_loop` dev harnesses drive the **real** qwen3:8b + live sandbox — also not mocks.
- **UI session isolation (`AssistantPanel.tsx`)**: any async call keyed to a conversation (`sendChat`, `analyzeOutput`, `approveProposal`) must capture `sid = sessionID` at call time and re-check `sessionIDRef.current === sid` before applying its result to `messages`/`proposals`/`activeTask` state. Without this guard, switching conversations while a request is in flight lets a stale response (or a stale WS `execution.status`/`agent.activity` event — filtered by `record.session_id !== sessionID`, no `'default'` special-case) paint into whatever session is now on screen, or — worse, in `auto` approval mode — auto-approve a proposal that belongs to a conversation the operator already left. `loadHistory` resets all session-scoped state (`proposals`, `activeTask`, `isLoading`, the auto-feedback refs) *synchronously* on switch, before the network round-trip, for the same reason. When adding a new async action in this file, follow the same pattern.
- **Shell output streaming and heartbeats**: queued shell commands use `executor.RunScopedCommandStream` → `sandbox.Bridge.ExecStream`. `execution.Store` emits ephemeral `output_chunk`/`output_source` fields under `type: execution.status`, `event: execution.output`; running heartbeats carry `elapsed_secs`. These transient fields are not persisted or placed on terminal records; final capped output/error buffers are authoritative. `Bridge.Exec` uses the same implementation with no callback, so synchronous direct exec callers receive their result on exit. MCP tools have their own response/terminal-read path; do not infer that all tools stream. Preserve threat quarantine when handling raw chunks.
- **UI i18n (build-time only, no runtime switch)**: every user-facing string in `ui/src` goes through `t('key')` from `ui/src/shared/i18n/strings.ts` — a dependency-free dictionary (`en`/`tr`), not a library like react-i18next. `tr` is typed as `satisfies Record<keyof typeof en, string>`, so a translation missing a key (or carrying a stray extra one) is a compile error, not a silent runtime fallback; `strings.test.ts` is a runtime backstop for the same invariant (mirrors `internal/security/invariants`' executable-fitness-function pattern). The active language is resolved **once at module load** from `import.meta.env.VITE_YUMMI_LANG` (defaults to `tr` if unset, e.g. bare `npm run dev`/`vitest` — an intentional asymmetry from the Makefile's own `YUMMI_LANG ?= en` default, so existing dev workflows don't silently flip language) — there is no in-app language toggle by design. `VITE_YUMMI_LANG` is set from `make conf`'s `YUMMI_LANG` choice, exported by the Makefile, and reaches both UI build paths: natively (`_build-ui`, `dev-ui`) and via Docker (`docker-compose.yml`'s `yummi-ui` build passes it as `ui/Dockerfile`'s `ARG VITE_YUMMI_LANG`, since a `docker build` doesn't see the host shell's environment otherwise). When adding new UI-facing text, add the key to both `en` and `tr` in `strings.ts` rather than inlining a literal string — a plain hardcoded string bypasses the whole mechanism and reintroduces exactly the TR/EN mixing this pattern exists to prevent.
- **Wire-exposure discipline on internal/diagnostic fields**: any Go struct field that can hold raw, target-controlled, or otherwise not-for-display content needs `json:"-"` — not just `omitempty` — because `omitempty` still serializes a populated value, it only hides an empty one. `agent.ActivityEvent.Detail` (`orchestrator/internal/agent/activity.go`) is the model to follow: its own doc comment states it can carry model rationale or compressed target output and is deliberately excluded from every REST/WS response, kept only for server-side diagnostics. `agent.Task.ObservationHistory` was found with the exact same stated intent ("holds full tool outputs for audit") but no `json:"-"` — currently harmless only because nothing populates it yet, but a landmine for whoever wires it up next. Now fixed to match `Detail`'s pattern. When adding a new field for server-side/audit-only data, default to `json:"-"` and only drop it if the field is genuinely meant for the UI.

## Recorded environment limits

- **Docker daemon access**: the host engine must be available. Compose mounts its socket only into the constrained broker; orchestrator uses `DOCKER_HOST=tcp://docker-proxy:2375`. Native development may use a local Docker socket/context.
- **macOS gateway**: gateway is Linux/nftables-only by design (no cross-platform abstraction), but on macOS via Docker Desktop it runs as a container on Docker Desktop's Linux VM — a real Linux kernel with `nf_tables` support — so nftables enforcement works via the normal `make start`/docker-compose path. This was verified end-to-end (FORWARD-chain DROP-all + single-target Allow correctly gated a real connection). The one path that genuinely does NOT enforce anything is running the compiled gateway binary directly on the macOS host outside Docker (e.g. bare `cargo run`) — there's no Linux kernel there at all, so don't treat that specific invocation as a security boundary test.
- **Docker Desktop VM networking (observed on Apple Silicon, multiple Docker Desktop versions)**: a container forwarding traffic between two custom Docker bridge networks — exactly gateway's role between `yummi_internal` and `yummi_external` — can have *all* of its forwarded traffic silently dropped by Docker Desktop's own vpnkit-based VM networking layer specifically when the source network has `internal: true` set, even though gateway's own nftables rules (verified correct) never see the problem. Isolated with a controlled experiment changing only that one flag; see `docs/installation.md`'s [Docker Desktop VM networking callout](docs/installation.md#docker-desktop-internal-network-bug) for the full reproduction and what was ruled out. Availability bug, not a fail-open — gateway's decisions stay correct, the traffic just never arrives. **Confirmed fixed by switching to [OrbStack](https://orbstack.dev/)** — identical code, identical `docker-compose.yml` (`internal: true` unchanged), rebuilt and run unmodified, DNS resolution and a real external scan both worked end to end. Also unaffected on native Linux Docker Engine. Updating Docker Desktop itself did not fix it.
- **Podman on macOS (v6.0.2, `applehv`)**: the actual trust-boundary forwarding works correctly here (unlike Docker Desktop above) — verified with the same real-external-IP + interface-counter test. But two other, unrelated gaps currently block a working agent loop: (1) `aardvark-dns` refuses to forward DNS upstream for any `internal: true` network regardless of configured servers, so hostname-based commands fail to resolve inside sandbox (IP-based targets work fine); a fix exists (`--disable-dns` on the network + pointing `DOCKER_HOST` at `docker-proxy`'s static IP) but needs an out-of-band `podman network create` step docker-compose.yml can't express on its own. (2) `docker-proxy`'s `/var/run/docker.sock` mount doesn't reach a live Podman-API socket (503s on every request), so orchestrator can never `docker exec` into sandbox — unresolved. See `docs/installation.md`'s [Podman callout](docs/installation.md#podman-macos-gaps). OrbStack remains the only no-workarounds macOS recommendation.

## Implemented subsystems

- **Semantic retrieval** (implemented): `knowledge.SemanticRetrieval` (`orchestrator/internal/knowledge/semantic_retrieval.go`) ranks procedural memory by embedding similarity via `llm.Client.Embed` against Ollama's `/api/embeddings` (local model, e.g. `nomic-embed-text` — stays privacy-first, no cloud call); vectors stored as little-endian BLOBs in the same SQLite file `knowledge.Store` already owns, ranked with an in-process cosine-similarity scan (`embeddings.go`, no `sqlite-vec` dependency — fine at this data scale). Falls back to `StructuredRetrieval` whenever there's nothing to rank semantically (no embedder, a failed embed call, or an empty nearest-neighbor lookup) — same nil-safe-degradation pattern as `Resilient`.
- **Auto-skill-creation** (implemented — `orchestrator/internal/skillcurator`): candidate skills are drafted by a background curator (`curator.go`'s `ProposeFromTask`, fired from `finishNode`) from a task's trajectory, never by the live agent loop. The trajectory is sanitized first (`sanitize.go`: IP/URL/email/secret redaction, bounded lengths, ANSI-stripped) and then scanned with the *same* `threatscan.FirstThreatMessage(..., ScopeStrict)` that `skill.LoadDir` already runs on hand-authored skill YAML. A cheap keyword-overlap check (`isDuplicate`) skips near-duplicates of an already-live or already-pending skill. Drafts land in a quarantined `pending_skills` store (origin `"auto"`), never auto-loaded into `Registry`. Only an explicit operator approval (`POST /api/v1/skills/pending/{id}/approve`, `handlers_skills.go`) promotes a draft — via an atomic claim → temp-file-write+fsync+rename → registry-upsert → commit sequence, so a failure at any step leaves the draft "failed," never falsely "approved" — into `YUMMI_AUTO_SKILL_DIR` (Compose: `/var/lib/yummi/orchestrator/skills`) that startup loading picks up; the mounted built-in persona directory stays read-only. Curator LLM calls run through `skillcurator.Worker`, a bounded non-blocking queue with a per-job timeout, so a full or slow queue can never delay task completion.
- **Red Team specialized sub-agents & skills (implemented).** The specialist roster (`persona/v0.1/subagents/*.yaml`, covering Active Directory, ADCS, Kerberos, Web/APIs, Cloud/K8s, Binary RE, Deserialization, Firmware/IoT, RF/SDR, AI Red Teaming, and APT Emulation) backed by declarative skills covering full-spectrum offensive security methodologies, built on the depth-1 handoff + shared Knowledge Store architecture. Each role carries a `routing_hint` the delegator uses to route work.
- **Mid-task natural language operator steering (implemented).** Active tasks can be steered mid-flight by sending chat messages in the UI. `handlers_chat.go` routes user messages directly to `Task.PendingGuidance`, which is injected at highest priority as `[OPERATOR GUIDANCE]` on the subsequent step without restarting or duplicating tasks.
- **Persistent tmux terminal sessions over MCP (implemented).** The sandbox MCP server provides 7 terminal tools (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`) backed by tmux, supporting interactive CLI commands (`sqlmap`, `msfconsole`, `gdb`), background listeners, and long-running operations.
- **License**: Apache License 2.0 (decided; see `LICENSE`, root README, and `gateway/Cargo.toml`'s `license` field). Chosen over MIT for the explicit patent grant + patent-retaliation clause, which matters for a security tool and for corporate contributors whose legal teams require it.

## Other entry points worth knowing about

- `docs/` (start at `docs/README.md`) is the canonical, kept-current reference set — it supersedes the per-module READMEs (`orchestrator/README.md`, `gateway/README.md`, `sandbox/README.md`, `ui/README.md`, `persona/README.md`) for current behavior. Dated plans, audits, release notes and test results are historical snapshots, not current verification.
- `docs/architecture.md` has more detail on trust-boundary flow and the agentic loop's status state machine if you need it.

## Commit Message Convention

Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`, `build:`, `refactor:`). Do **not** add a `Co-Authored-By: Codex...` (or any AI co-author) trailer — permanently disabled per operator preference, all projects.
