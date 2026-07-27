← [Back to README](../README.md) · [Docs index](README.md)

# Architecture

Yummi is **five runtime components** working across **three build ecosystems** (Go, Rust, Node/React). This page explains what each component is, why it exists as a *separate* process rather than a library call, and exactly how a command travels from an operator's intent to a real command running against a real target.

If you take away one sentence from this whole page, take this one: **the AI never runs a command. It only ever proposes one, and a component that contains no AI decides whether it is allowed to happen.**

## Table of contents

- [The five components](#five-components)
- [Why five processes and not one binary](#why-separate-processes)
- [Network topology](#network-topology)
- [Data flow: a single chat command](#flow-single-command)
- [Data flow: an agentic task](#flow-agentic-task)
- [Data flow: the approval gate](#flow-approval-gate)
- [Where each build ecosystem lives](#build-ecosystems)
- [What is *not* in this repository](#not-in-scope)

---

## The five components <a id="five-components"></a>

```
                         ┌─────────────────────┐
        operator ───────▶│   ui (React/nginx)   │  :3000
                         └──────────┬───────────┘
                                    │ /api, /ws (proxied)
                         ┌──────────▼───────────┐
        CLI / TUI ──────▶│  orchestrator (Go)   │  :9080 (127.0.0.1 only)
                         │  conductor + agent    │
                         └─────┬────────┬────────┘
                                │        │
                    IPC (Unix socket,    │ docker exec (via docker-proxy)
                    length-prefixed JSON)│
                                │        │
                         ┌──────▼──────┐ │
                         │gateway (Rust)│ │
                         │fail-closed   │ │
                         │  nftables    │ │
                         └──────┬───────┘ │
                                │         │
                    FORWARD chain         │
                    (allow/deny)          │
                                │         │
                         ┌──────▼─────────▼─────┐
                         │     sandbox (Kali)     │  no direct internet route;
                         │                        │  network egress gated by
                         │                        │  gateway's FORWARD chain
                         └────────────────────────┘
```

Two independent arrows reach `sandbox`, and they do two different jobs: `docker exec` (via `docker-proxy`) is *how a command gets run inside the container*; gateway's `FORWARD` chain is *what that command is allowed to reach once it's running*. The two never substitute for each other — a command still executes even if gateway would eventually block its network target; gateway just stops that specific connection when the command tries to make it.

| # | Component | Language / runtime | What it is allowed to do | What it can *never* do |
|---|---|---|---|---|
| 1 | **orchestrator** | Go binary (built with `CGO_ENABLED=1` for the SQLite driver, so it links against the host's C library — not a fully static binary) | Talk to the LLM, run the agent's state machine, serve the REST/WS API, ask gateway "is this target in scope?" | Open a raw socket to the internet, or write an nftables rule itself |
| 2 | **gateway** | Rust binary | Install/enforce nftables `FORWARD` rules, decide egress verdicts, hash-chain an audit log | Run a pentest tool, talk to the LLM, know anything about chat/tasks |
| 3 | **sandbox** | Kali Linux container (read-only rootfs) | Run pentest tools when `docker exec`'d into by orchestrator | Initiate outbound connections that gateway hasn't allowed — it has no route out except through gateway |
| 4 | **docker-proxy** | `tecnativa/docker-socket-proxy` | Forward exactly `docker exec`/`docker inspect`-shaped Docker API calls from orchestrator to the host socket | Start, stop, kill, build, or otherwise manage *any* container — every other Docker API resource family is explicitly zeroed |
| 5 | **ui** | React 18 + Vite, served by nginx | Render operator-facing dashboards, proxy `/api` and `/ws` to orchestrator | Talk to gateway or sandbox directly — it only ever talks to orchestrator |

A **sixth, alternative front-end** exists: the terminal UI (TUI), a Go binary (`./bin/yummi --tui`) that talks to the exact same orchestrator REST/WS API the web UI does. It is not a separate backend — see [CLI & TUI](modules/cli-and-tui.md).

> **Why this table matters more than it looks:** every row's "can never do" column is enforced by an *operating-system or container mechanism* (Linux capabilities, a read-only filesystem, an allowlisted API proxy, an isolated Docker network) — not by application code choosing to behave. That distinction is the difference between "the AI was told not to" and "the AI is physically unable to," and it is the whole reason this project exists. See [Security Model](security-model.md) for the full enforcement chain.

### docker-proxy — the component most documentation forgets <a id="docker-proxy"></a>

If you've read *any* older internal notes about this project (or a stale copy of this repo), you may see "four components." `docker-proxy` is real, it runs in `docker-compose.yml`, and skipping it in documentation would be actively misleading, because it closes a hole that would otherwise exist: orchestrator needs to run `docker exec` into the sandbox container, and the most obvious way to grant that is mounting `/var/run/docker.sock` straight into the orchestrator container. **Don't do that** — the Docker socket is root-equivalent to the whole host; a container with it mounted can create *any* container with *any* mount, which is a straightforward container-escape primitive.

Instead, `docker-proxy` sits between orchestrator and the real socket and is configured (`docker-compose.yml`, `docker-proxy` service) to allow only:

```yaml
CONTAINERS: 1   # GET /containers/* (needed to resolve the sandbox container)
EXEC:       1   # POST /containers/{id}/exec (this is `docker exec` itself)
POST:       1   # required for EXEC — creating+starting an exec instance are POST calls
# Every other resource family is explicitly 0: ALLOW_START, ALLOW_STOP,
# ALLOW_RESTARTS, BUILD, COMMIT, IMAGES, NETWORKS, SECRETS, SWARM, SYSTEM,
# VOLUMES, ...
```

`POST: 1` combined with `CONTAINERS: 1` would, on its own, also unlock generic container *lifecycle* control (start/stop/restart/kill/rename/prune) on every container reachable on the Docker network — that's why `ALLOW_START`/`ALLOW_STOP`/`ALLOW_RESTARTS` are explicitly zeroed alongside it. The allowlist is deliberately spelled out in full in `docker-compose.yml` (every resource family listed, even the ones set to `0`) rather than relying on the image's own defaults, so the permission surface is something you can read in one screen, not something you have to go verify against `tecnativa/docker-socket-proxy`'s changelog.

If you're auditing this repository for a security review: this is one of the first things worth checking still matches the compose file, since it is a narrow, security-critical allowlist that a careless future edit ("just add `IMAGES: 1`, I need it for something") could quietly widen.

---

## Why separate processes, not one binary <a id="why-separate-processes"></a>

It would be *simpler* to build one Go binary that both talks to the LLM and manages iptables/nftables rules. Yummi deliberately doesn't, for one reason: **process boundaries are the only kind of boundary that survives a crash or a bug.**

Consider what happens if the AI-facing code has a bug — a bad prompt injection, a parsing error, a genuinely malicious jailbreak that gets the model to try something it shouldn't. If firewall logic lives in the *same process*, that bug is one panic, one memory corruption, or one clever function call away from also compromising network policy. If firewall logic lives in a **separate, minimal, memory-safe (Rust) process that installs deny-all rules before it even opens its control socket**, then the AI-facing process crashing, hanging, or being tricked changes nothing about what the sandbox can reach — the rules gateway already installed keep applying whether or not gateway itself is still running.

This is also why gateway is written in **Rust** rather than Go, and why it is deliberately small (`gateway/src/` is a handful of tightly-scoped modules — `policy/`, `network/`, `ipc/`, `audit/`, `observer/`, `watchdog.rs`). To be precise about *why* Rust specifically, rather than reaching for a vague "memory-safe language" claim that would apply to Go too (Go is garbage-collected and bounds-checked — it doesn't have C-style buffer overflows either): the two concrete advantages that matter for a component managing kernel firewall rules are (1) no garbage collector, so there's no GC-pause jitter in the one process the sandbox's network connectivity depends on moment-to-moment, and (2) the borrow checker rules out a class of data races **at compile time** that Go can only catch with the `-race` detector at runtime, on code paths that happen to get exercised by a test. Neither point makes gateway "unbreakable" — it means the class of bug most likely to matter in a small, concurrent, kernel-adjacent process is caught earlier and more completely than Go's own memory model catches it. That, plus staying small enough that a human can actually read all of it in one sitting, is the actual argument — not a blanket "Rust is safer than Go."

---

## Network topology <a id="network-topology"></a>

```
yummi_internal (172.20.0.0/24, driver: bridge, internal: true — NO external route)
  ├─ gateway         172.20.0.2   (also serves as sandbox's DNS + default gateway)
  ├─ orchestrator    172.20.0.3
  ├─ ui              172.20.0.4
  ├─ docker-proxy    172.20.0.5
  └─ sandbox         172.20.0.10  (ONLY network membership — no yummi_external)

yummi_external (driver: bridge, normal — has a route to the internet)
  ├─ gateway
  ├─ orchestrator    (reaches Ollama on the host via host.docker.internal)
  └─ ui
```

The load-bearing detail: **`sandbox` is a member of `yummi_internal` only.** Docker Compose's `internal: true` network flag means containers on `yummi_internal` have no gateway-provided route to the outside world at all — not "blocked by a firewall rule that could theoretically be misconfigured," but architecturally absent. The *only* way sandbox traffic reaches anything outside `172.20.0.0/24` is by routing through `gateway` (`172.20.0.2`, which sandbox's entrypoint sets as its default route — see `sandbox/entrypoint.sh`) and then through whatever nftables `FORWARD` rules gateway has installed at that moment. If gateway crashes, sandbox doesn't fail open to the internet — it just has no route anywhere, because `yummi_internal` never had one to begin with. That's fail-closed at two independent layers (Docker network topology *and* nftables), not one.

DNS deserves its own callout because it's a common point of confusion: sandbox's `dns:` is set to Cloudflare (`1.1.1.1`, `1.0.0.1`) in `docker-compose.yml`, and orchestrator resolves hostnames through the same resolver (`orchestrator/internal/dnsresolve`) before ever calling gateway. **DNS resolution itself is not a trust boundary** — knowing that `example.com` is `93.184.216.34` grants nothing. The trust boundary is the *connection* to that resolved IP, which still has to cross gateway's `FORWARD` chain. Both sides using the same resolver exists purely so they *agree* on which IP a name means — if orchestrator resolved via one resolver and sandbox via another (e.g. Docker's embedded DNS), a hostname could round-robin to a different IP between the moment orchestrator opens an allow rule and the moment sandbox actually connects, silently reintroducing the exact class of bug this design avoids.

---

## Data flow: a single chat command <a id="flow-single-command"></a>

The simplest path — an operator asks for one thing in chat and the model proposes exactly one command:

```
1. UI  →  POST /api/v1/chat                       {"message": "scan 10.0.0.5 for open ports"}
2. orchestrator: conductor.Engine.ChatWithHistory() calls the LLM (Ollama)
3. LLM replies → ChatResponse{ intent: "suggest_command", proposals: [...] }
4. UI shows a proposal bar; operator clicks Approve
5. POST /api/v1/chat/approve
6. execution.Store.Enqueue()  →  executor.ProcessFunc drains it
7. executor.RunScopedCommand():
     a. scope.ExtractTarget(command)  →  "10.0.0.5"
     b. gateway.CheckEgress("10.0.0.5")  →  verdict: allow | block | needs_approval
     c. if allow: gateway.AddTemporaryRule("10.0.0.5/32", ttl=3600s)  (nftables rule opens)
     d. sandbox.Bridge.Exec()  →  `docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"`
8. WS /ws/events fires execution.status events the whole way through (queued → running → done)
```

Step 7 is where every real decision happens, and it is the **only** function in the whole Go codebase that is allowed to make it — `executor.RunScopedCommand` (`orchestrator/internal/executor/executor.go`). Every code path that runs a pentest command, whether it came from a one-shot chat approval or from deep inside the agent's task loop, converges on this one function. That's a deliberate invariant, not an accident of how the code happened to get organized — see [Sandbox Execution](agent/sandbox-execution.md) for why it's structured this way and what would go wrong if it weren't.

---

## Data flow: an agentic task <a id="flow-agentic-task"></a>

The richer path — the operator gives a goal, and the agent plans and executes multiple steps autonomously (subject to approval gates):

```
1. UI  →  POST /api/v1/chat   (conductor detects a start_task intent from the goal)
2. agent.Runner.CreateAndPlan()   — one LLM call produces a numbered plan
3. agent.Runner.Start()  →  r.loop()  →  graph.Execute(state, nodeStep)
4. The graph state machine walks nodes via a routing table (node → edge → next node)
   until it reaches a terminal node: finish or ask.
5. WS /ws/events fires task.status events throughout; the completion summary is
   returned to chat once the task reaches a terminal node.
```

The graph is the real engine here, and it's detailed in full — every node, every edge, why it's a state machine and not a loop — in [The Agent Graph](agent/graph-and-nodes.md). The short version: `step` asks the model what to do next, and depending on what comes back, the state machine can route to running a tool, delegating to a sub-agent, persisting a memory, pausing for an operator decision, compressing context, or recovering from an LLM hiccup — each of those is a distinct node, not a branch buried inside one giant function.

---

## Data flow: the approval gate <a id="flow-approval-gate"></a>

Both flows above can hit the same gate — any command whose risk classification is medium or higher:

```
risk ≥ medium  →  gate.Request() blocks the task's own goroutine (not the whole server)
              →  task/proposal status becomes "awaiting_approval"
              →  WS pushes { pending_command, pending_risk } to every connected UI
              →  operator resolves: POST /api/v1/tasks/{id}/approve  (agent path)
                                  or POST /api/v1/chat/approve        (single-command path)
              →  gate.Resolve(verdict)  →  the blocked goroutine wakes up and resumes
```

The gate (`orchestrator/internal/agent/gate.go`) is intentionally *just* a Go channel wrapped in a small struct — there is no message queue, no polling, no separate approval microservice. A proposal that nobody answers doesn't block forever, though: `conductor.ApprovalStore` runs a 20-minute TTL purger independently of the gate, so a stale proposal a UI never resolved eventually expires rather than sitting as a landmine for someone to accidentally approve a week later.

---

## Where each build ecosystem lives <a id="build-ecosystems"></a>

Yummi's code is organized by **implementation stack**, and this documentation set mirrors that split deliberately — see [Modules](modules/) for the deep-dive on each:

| Ecosystem | Component(s) | Entry point for reading the code | Docs page |
|---|---|---|---|
| **Go — two separate modules** | CLI/TUI (root `go.mod`) + orchestrator (`orchestrator/go.mod`, its own `go.mod`) | `cmd/yummi/main.go`, `orchestrator/cmd/orchestrator/main.go` | [Orchestrator module](modules/orchestrator.md), [CLI & TUI](modules/cli-and-tui.md) |
| **Rust** | gateway | `gateway/src/main.rs` | [Gateway module](modules/gateway.md) |
| **Node / React** | ui | `ui/src/main.tsx` | [UI module](modules/ui.md) |
| **Shell / declarative** | sandbox image, persona YAML | `sandbox/Dockerfile`, `persona/v0.1/` | [Sandbox execution](agent/sandbox-execution.md), [Persona & Skills](persona/persona-and-skills.md) |

**Why two Go modules and not one?** `go.mod` at the repo root (`github.com/n0connect/yummi`) is the CLI/TUI only — it depends on terminal UI libraries (`bubbletea`, `lipgloss`) that the server has no business importing. `orchestrator/go.mod` (`github.com/n0connect/yummi/orchestrator`) is everything AI/API/execution-related, with its own dependency set (`chi`, `mattn/go-sqlite3`, etc.). Splitting them means `go test ./...` from the repo root **silently skips every orchestrator test** — it's a different module, so you must `cd orchestrator` (or prefix `cd orchestrator &&`) first. This is a common first surprise; see the [Installation](installation.md) and [Development](development.md) pages for the exact commands.

---

## What is *not* in this repository <a id="not-in-scope"></a>

Being explicit about scope is part of being an honest architecture document:

- **Ollama itself** is not part of this repo — it's a separate, independently-installed LLM runtime that orchestrator calls over HTTP. Yummi ships zero model weights and makes zero cloud LLM calls; everything stays on the operator's own machine. See [Installation § Ollama setup](installation.md#ollama-setup).
- **`internal/mcp`** (`orchestrator/internal/mcp/client.go`) is real, compiles, and is tested — but it is not imported by any production code path today. It is a dormant JSON-RPC 2.0 stdio client for external MCP servers, kept for a future integration that hasn't landed yet. Its existence does not currently expand the agent's capabilities or its attack surface.
- **A cross-platform network controller for gateway** does not exist by design. `gateway/src/network/mod.rs` states outright that the old cross-platform trait and a macOS no-op stub were removed — `nftables.rs` is the *only* concrete controller, and it requires a genuine Linux kernel with `nf_tables` support. See [Installation § Platform support](installation.md#platform-support) for exactly what this means on macOS and Windows.
