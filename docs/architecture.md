← [Back to README](../README.md) · [Docs index](README.md)

# Architecture

Yummi is **five runtime components** working across **three build ecosystems** (Go, Rust, Node/React). This page explains what each component is, why it exists as a *separate* process rather than a library call, and exactly how a command travels from an operator's intent to a real command running against a real target.

If you take away one sentence from this whole page, take this one: **the model proposes commands; non-LLM code owns approval state, target checks, execution, and network enforcement.**

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
        CLI ────────────▶│  orchestrator (Go)   │  :9080 (127.0.0.1 only)
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

| # | Component | Language / runtime | Primary responsibility | Capability boundary in the shipped stack |
|---|---|---|---|---|
| 1 | **orchestrator** | Go binary (built with `CGO_ENABLED=1` for the SQLite driver, so it links against the host's C library — not a fully static binary) | Talk to the LLM, run the agent's state machine, serve the REST/WS API, ask gateway "is this target in scope?" | Open a raw socket to the internet, or write an nftables rule itself |
| 2 | **gateway** | Rust binary | Install/enforce nftables `FORWARD` rules, decide egress verdicts, hash-chain an audit log | Run a pentest tool, talk to the LLM, know anything about chat/tasks |
| 3 | **sandbox** | Kali Linux container (read-only rootfs) | Run pentest tools when `docker exec`'d into by orchestrator | Initiate outbound connections that gateway hasn't allowed — it has no route out except through gateway |
| 4 | **docker-proxy** | `tecnativa/docker-socket-proxy` | Forward exactly `docker exec`/`docker inspect`-shaped Docker API calls from orchestrator to the host socket | Start, stop, kill, build, or otherwise manage *any* container — every other Docker API resource family is explicitly zeroed |
| 5 | **ui** | React 18 + Vite, served by nginx | Render operator-facing dashboards, proxy `/api` and `/ws` to orchestrator | Talk to gateway or sandbox directly — it only ever talks to orchestrator |

Yummi is managed entirely from this web UI — there is deliberately no alternative front-end (a native desktop shell and a terminal UI both existed at one point and were removed in favor of a single, simpler operator surface). The root-module `yummi` binary ([CLI](modules/cli.md)) only handles Docker lifecycle, diagnostics, and opening this same web UI in an app-style browser window; it is not a second client of the API.

> These are defense-in-depth boundaries, not universal impossibility claims. Linux capabilities, a read-only filesystem, the Docker API proxy, and network topology constrain the normal shipped processes; container-root compromise, host compromise, an `insecure-bridge` run, or operator reconfiguration changes that threat model. See [Security Model](security-model.md) for the enforcement chain and residual limits.

### docker-proxy — the component most documentation forgets <a id="docker-proxy"></a>

`docker-proxy` is a real fifth runtime component in `docker-compose.yml`. It exists because mounting `/var/run/docker.sock` directly into orchestrator would expose a root-equivalent host control surface.

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

It would be *simpler* to build one Go binary that both talks to the LLM and manages iptables/nftables rules. Yummi separates them so a failure in model-facing Go code does not automatically share the gateway's privileges or mutable state.

Consider what happens if the AI-facing code has a bug — a bad prompt injection, a parsing error, a genuinely malicious jailbreak that gets the model to try something it shouldn't. If firewall logic lives in the *same process*, that bug is one panic, one memory corruption, or one clever function call away from also compromising network policy. If firewall logic lives in a **separate, minimal, memory-safe (Rust) process that installs deny-all rules before it even opens its control socket**, then the AI-facing process crashing, hanging, or being tricked changes nothing about what the sandbox can reach — the rules gateway already installed keep applying whether or not gateway itself is still running.

This is also why gateway is written in **Rust** rather than Go, and why it is deliberately small (`gateway/src/` is a handful of tightly-scoped modules — `policy/`, `network/`, `ipc/`, `audit/`, `observer/`, `watchdog.rs`). To be precise about *why* Rust specifically, rather than reaching for a vague "memory-safe language" claim that would apply to Go too (Go is garbage-collected and bounds-checked — it doesn't have C-style buffer overflows either): the two concrete advantages that matter for a component managing kernel firewall rules are (1) no garbage collector, so there's no GC-pause jitter in the one process the sandbox's network connectivity depends on moment-to-moment, and (2) the borrow checker rules out a class of data races **at compile time** that Go can only catch with the `-race` detector at runtime, on code paths that happen to get exercised by a test. Neither point makes gateway "unbreakable" — it means the class of bug most likely to matter in a small, concurrent, kernel-adjacent process is caught earlier and more completely than Go's own memory model catches it. That, plus staying small enough that a human can actually read all of it in one sitting, is the actual argument — not a blanket "Rust is safer than Go."

---

## Network topology <a id="network-topology"></a>

```
yummi_internal (172.20.0.0/24, driver: bridge, internal: true — NO external route)
  ├─ gateway         172.20.0.2   (sandbox's configured default gateway)
  ├─ orchestrator    172.20.0.3
  ├─ ui              172.20.0.4
  ├─ docker-proxy    172.20.0.5
  └─ sandbox         172.20.0.10  (ONLY network membership — no yummi_external)

yummi_external (driver: bridge, normal — has a route to the internet)
  ├─ gateway
  ├─ orchestrator    (reaches Ollama on the host via host.docker.internal)
  └─ ui
```

The load-bearing detail: **`sandbox` is a member of `yummi_internal` only.** `internal: true` removes Docker's ordinary external gateway. The sandbox entrypoint then deliberately replaces its default route with the multi-homed `gateway` container (`172.20.0.2`), so external packets must traverse gateway's forwarding namespace and nftables rules. If that router disappears, the configured next hop is unavailable rather than transparently falling back to Docker's normal bridge gateway. The opt-in `insecure-bridge` Compose overlay weakens this property by restoring ordinary bridge egress and is documented as a compatibility workaround, not the default posture.

DNS deserves its own callout because it's a common point of confusion: sandbox's `dns:` is set to Cloudflare (`1.1.1.1`, `1.0.0.1`) in `docker-compose.yml`, and orchestrator resolves hostnames through the same resolver (`orchestrator/internal/dnsresolve`) before ever calling gateway. **DNS resolution itself is not a trust boundary** — knowing that `example.com` is `93.184.216.34` grants nothing. The trust boundary is the *connection* to that resolved IP, which still has to cross gateway's `FORWARD` chain. Both sides using the same resolver exists purely so they *agree* on which IP a name means — if orchestrator resolved via one resolver and sandbox via another (e.g. Docker's embedded DNS), a hostname could round-robin to a different IP between the moment orchestrator opens an allow rule and the moment sandbox actually connects, silently reintroducing the exact class of bug this design avoids.

---

## Data flow: a single chat command <a id="flow-single-command"></a>

The simplest path — an operator asks for one thing in chat and the model proposes exactly one command:

```
1. UI  →  POST /api/v1/chat                       {"message": "scan 10.0.0.5 for open ports"}
2. orchestrator: conductor.Engine.ChatWithHistory() calls the LLM (Ollama)
3. LLM replies → ChatResponse{ intent: "suggest_command", proposals: [...] }
4. UI shows a proposal bar; in ask_permission the operator clicks Approve (auto resolves it immediately)
5. POST /api/v1/chat/approve
6. execution.Store.Enqueue()  →  executor.ProcessFunc drains it
7. executor.RunScopedCommand():
     a. scope.ExtractTargets(command)  →  ["10.0.0.5"]
     b. for hostnames: gateway.CheckHostname(), then Cloudflare DNS resolution
     c. gateway.CheckEgress(IP) for every resolved/explicit target  → allow | block | needs_approval
     d. only if all allow: gateway.AddTemporaryRule("10.0.0.5/32", ttl=3600s)
     e. sandbox.Bridge.Exec()  →  `docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"`
8. WS /ws/events fires execution.status events the whole way through (queued → running → done)
```

Step 7e is a single blocking call — `sandbox.Bridge.Exec` retains at most 1 MiB from each stdout/stderr stream and returns only once the process exits; there is no incremental output streaming. For a command that legitimately runs for a long time, `execution.Store.runWithHeartbeat` republishes `"execution.running"` every few seconds with `ElapsedSecs` set. This is liveness feedback, not output streaming; the retained bytes arrive only at completion.

`executor.RunScopedCommand` is the shared decision point for scheduled commands from chat, agent tasks, and direct exec. The human PTY terminal is the documented exception: it reaches the same sandbox but does not run pre-exec target extraction. See [Sandbox Execution](agent/sandbox-execution.md).

---

## Data flow: an agentic task <a id="flow-agentic-task"></a>

The richer path — the operator gives a goal, and the agent plans and executes multiple steps autonomously (subject to approval gates):

```
1. UI  →  POST /api/v1/chat   (conductor detects a start_task intent from the goal)
2. agent.Runner.CreateAndPlan()   — one LLM call produces a numbered plan
3. agent.Runner.Start()  →  r.loop()  →  graph.Execute(state, nodeStep)
4. The graph state machine walks nodes via a routing table (node → edge → next node)
   until it reaches a terminal node: finish or ask. A single step can request up to
   four depth-1 `HANDOFF`s, bounded by a shared process-local concurrency quota
   (default 3 running specialists at once, regardless of how many are requested);
   child tasks share the parent's Knowledge Store and return sanitized summaries.
5. `finish` emits a server-recorded evidence digest and persists per-finding
   verdicts alongside the legacy flat findings list.
6. WS /ws/events fires task.status events throughout; the completion summary is
   returned to chat once the task reaches a terminal node.
```

The graph is the real engine here, and it's detailed in full — every node, every edge, why it's a state machine and not a loop — in [The Agent Graph](agent/graph-and-nodes.md). The short version: `step` asks the model what to do next, and depending on what comes back, the state machine can route to running a tool, delegating to a sub-agent, persisting a memory, pausing for an operator decision, compressing context, or recovering from an LLM hiccup — each of those is a distinct node, not a branch buried inside one giant function.

---

## Data flow: the approval gate <a id="flow-approval-gate"></a>

Command approval is mode-driven. In `ask_permission`, every agent-task command pauses; in `auto`, agent-task commands proceed without this gate. Risk classification remains visible metadata and the single-command validator still computes `requires_approval`, but the UI's `auto` mode resolves arriving proposals immediately through the same approval endpoint.

```
ask_permission → gate.Request() blocks the task's own goroutine (not the whole server)
              →  task/proposal status becomes "awaiting_approval"
              →  WS pushes { pending_command, pending_risk } to every connected UI
              →  operator resolves: POST /api/v1/tasks/{id}/approve  (agent path)
                                  or POST /api/v1/chat/approve        (single-command path)
              →  gate.Resolve(verdict)  →  the blocked goroutine wakes up and resumes
```

The agent-task gate (`orchestrator/internal/agent/gate.go`) is intentionally *just* a Go channel wrapped in a small struct — there is no message queue, polling, or separate approval microservice. Single-command chat proposals use `conductor.ApprovalStore`; its 20-minute TTL purger removes stale proposals a UI never resolved.

---

## Where each build ecosystem lives <a id="build-ecosystems"></a>

Yummi's code is organized by **implementation stack**, and this documentation set mirrors that split deliberately — see [Modules](modules/) for the deep-dive on each:

| Ecosystem | Component(s) | Entry point for reading the code | Docs page |
|---|---|---|---|
| **Go — two separate modules** | CLI (root `go.mod`) + orchestrator (`orchestrator/go.mod`, its own `go.mod`) | `cmd/yummi/main.go`, `orchestrator/cmd/orchestrator/main.go` | [Orchestrator module](modules/orchestrator.md), [CLI](modules/cli.md) |
| **Rust** | gateway | `gateway/src/main.rs` | [Gateway module](modules/gateway.md) |
| **Node / React** | ui | `ui/src/main.tsx` | [UI module](modules/ui.md) |
| **Shell / declarative** | sandbox image, persona YAML | `sandbox/Dockerfile`, `persona/v0.1/` | [Sandbox execution](agent/sandbox-execution.md), [Persona & Skills](persona/persona-and-skills.md) |

**Why two Go modules and not one?** `go.mod` at the repo root (`github.com/n0connect/yummi`) is the CLI only — it depends on terminal UI libraries (`bubbletea`, `lipgloss`) that the server has no business importing. `orchestrator/go.mod` (`github.com/n0connect/yummi/orchestrator`) is everything AI/API/execution-related, with its own dependency set (`chi`, `mattn/go-sqlite3`, etc.). Splitting them means `go test ./...` from the repo root **silently skips every orchestrator test** — it's a different module, so you must `cd orchestrator` (or prefix `cd orchestrator &&`) first. This is a common first surprise; see the [Installation](installation.md) and [Development](development.md) pages for the exact commands.

---

## What is *not* in this repository <a id="not-in-scope"></a>

Being explicit about scope is part of being an honest architecture document:

- **Ollama itself** is not part of this repo — it's a separate, independently-installed LLM runtime that orchestrator calls over HTTP. Yummi ships zero model weights and makes zero cloud LLM calls; everything stays on the operator's own machine. See [Installation § Ollama setup](installation.md#ollama-setup).
- **`internal/mcp`** (`orchestrator/internal/mcp/client.go`) is real, compiles, and is tested — but it is not imported by any production code path today. It is a dormant JSON-RPC 2.0 stdio client for external MCP servers, kept for a future integration that hasn't landed yet. Its existence does not currently expand the agent's capabilities or its attack surface.
- **A cross-platform network controller for gateway** does not exist by design. `gateway/src/network/mod.rs` states outright that the old cross-platform trait and a macOS no-op stub were removed — `nftables.rs` is the *only* concrete controller, and it requires a genuine Linux kernel with `nf_tables` support. See [Installation § Platform support](installation.md#platform-support) for exactly what this means on macOS and Windows.
