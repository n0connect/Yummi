← [Back to README](../README.md) · [Docs index](README.md)

# API Reference

The full REST + WebSocket surface orchestrator exposes, as wired in `orchestrator/internal/api/routes.go` — this page is a map of *what exists and why*, not a request/response schema dump (read the handler source in `orchestrator/internal/api/handlers_*.go` for exact JSON shapes when you're integrating against a specific endpoint). Every route below is served under **`http://127.0.0.1:9080`** by default — bound to localhost only, not exposed to your LAN, by design.

## Table of contents

- [Conventions](#conventions)
- [Sessions](#sessions)
- [Targets & findings](#targets-findings)
- [TRACE — observability (read-only)](#trace)
- [Chat](#chat)
- [Agent tasks](#agent-tasks)
- [Executions](#executions)
- [Gateway management](#gateway)
- [Sandbox](#sandbox)
- [System](#system)
- [WebSockets](#websockets)

---

## Conventions <a id="conventions"></a>

- Every route lives under `/api/v1`, except the two WebSocket upgrade endpoints (`/ws/terminal`, `/ws/events`) and the plain health check (`/healthz`).
- Errors follow **RFC 7807** problem-detail JSON (`Content-Type: application/problem+json`) — expect `{"type":"about:blank","title":"...","status":<code>,"detail":"..."}`, not a bespoke error format per endpoint. `type` is always the literal string `"about:blank"` today — no per-error-kind URIs are defined yet, so don't write client code that branches on it.
- `{id}` / `{sessionID}` path segments are validated (`isValidSessionID`) before touching any store — a malformed ID is a `400`, not a store lookup that happens to fail.
- CORS and WebSocket origin headers are both validated against localhost server-side (`CORSMiddleware`, and origin checks inside the WS upgrade handlers) — this is a deliberate CSWSH (cross-site WebSocket hijacking) mitigation, not an oversight that happens to work for local dev.

---

## Sessions <a id="sessions"></a>

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sessions` | List all sessions |
| `POST` | `/sessions` | Create a session |
| `GET` | `/sessions/{id}` | Get one session |
| `GET` | `/sessions/{id}/messages` | Chat history for a session |
| `PATCH` | `/sessions/{id}` | Update session metadata |
| `DELETE` | `/sessions/{id}` | Delete a session |

## Targets & findings <a id="targets-findings"></a>

Owned by the [Knowledge Store](agent/memory-and-knowledge.md), not by the session manager directly:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sessions/{sessionID}/targets` | List declared/discovered in-scope targets |
| `POST` | `/sessions/{sessionID}/targets` | Declare a target |
| `GET` | `/sessions/{sessionID}/findings` | List structured findings |
| `POST` | `/sessions/{sessionID}/findings` | Add a finding manually (operator-entered, `source: "manual"`) |

## TRACE — observability (read-only) <a id="trace"></a>

Backs the [Trace tab](usage-guide.md#tab-trace) — every one of these is a pure read except the two pending-skill decision endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sessions/{sessionID}/activity` | Full activity timeline (plan/command/result/memory/skill/handoff/finding/ask/finish events) |
| `GET` | `/sessions/{sessionID}/context` | Persistent `REMEMBER`-written memory for this session |
| `GET` | `/tactics` | Target-scrubbed procedural memory (cross-session) |
| `GET` | `/skills` | Currently loaded skill registry |
| `GET` | `/skills/pending` | Curator-drafted skills awaiting operator review |
| `POST` | `/skills/pending/{id}/approve` | Approve a drafted skill — writes it into `persona/v0.1/skills/` and adds it to the live registry |
| `POST` | `/skills/pending/{id}/reject` | Reject a drafted skill |
| `GET` | `/agent/roles` | Available specialist sub-agent roles |
| `GET` | `/sessions/{sessionID}/audit` | Session-level audit trail (distinct from gateway's own network audit log below) |

## Chat <a id="chat"></a>

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/chat` | Send a message; may return a single command proposal or start an agent task |
| `POST` | `/chat/approve` | Approve/deny a single-command proposal |

## Agent tasks <a id="agent-tasks"></a>

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/agent/mode` | Get the agent's approval mode (`auto` / `ask_permission`) |
| `POST` | `/agent/mode` | Set the agent's approval mode |
| `POST` | `/tasks` | Create a task (goal → plan, does not start it) |
| `GET` | `/tasks/{id}` | Get task state |
| `GET` | `/sessions/{sessionID}/tasks` | List tasks for a session |
| `POST` | `/tasks/{id}/start` | Start executing a planned task |
| `POST` | `/tasks/{id}/stop` | Stop a running task |
| `POST` | `/tasks/{id}/approve` | Resolve a task's pending approval gate (command risk, or threat-scan hold) |

## Executions <a id="executions"></a>

The lower-level record of every individual command run (both from single-command chat approvals and from inside agent tasks):

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sessions/{sessionID}/executions` | List executions for a session |
| `GET` | `/executions/{id}` | Get one execution record |
| `POST` | `/executions/{id}/cancel` | Cancel a running/queued execution |
| `POST` | `/executions/{id}/resolve-egress` | Resolve an **Ask-mode** egress hold — see [Security Model § Egress modes](security-model.md#egress-modes) |
| `POST` | `/executions/{id}/resolve-threat` | Resolve a flagged single-command output hold — body `{approved: true|false}`; approval releases the sanitized result to the chat feedback flow, denial keeps it out of the LLM context |

## Gateway management <a id="gateway"></a>

Backs the [Gateway tab](usage-guide.md#tab-gateway) — every one of these is an orchestrator REST endpoint that internally speaks the gateway's own IPC protocol (Unix socket, length-prefixed JSON) on your behalf; the browser never talks to gateway directly:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/gateway/status` | Reachability + current egress mode |
| `GET` | `/gateway/rules` | List active nftables allow/block rules (tagged by `source`) |
| `POST` | `/gateway/rules` | Add a manual temporary rule — body `{network, ttl_secs, reason}` (`reason` is required) |
| `DELETE` | `/gateway/rules/{id}` | Remove a rule |
| `GET` | `/gateway/audit` | Gateway's own tamper-evident audit log — `?limit=N` |
| `GET` | `/gateway/fingerprint` | Current fingerprint-masking config (TTL normalize / MAC randomization) |
| `GET` | `/gateway/egress-mode` | Current egress mode |
| `POST` | `/gateway/egress-mode` | Set egress mode (`block` / `allow` / `ask`) — **does not persist across gateway restarts** |
| `GET` | `/gateway/hostname-policy` | Current `hostname_allow`/`hostname_block` glob lists |
| `POST` | `/gateway/hostname-policy` | Add a pattern to the `allow` or `block` list — body `{list, pattern}`. **In-memory only, resets to `gateway/config/default.yaml`'s lists on gateway restart** — same non-persistence contract as egress mode |
| `DELETE` | `/gateway/hostname-policy` | Remove a pattern — same body shape, same non-persistence caveat |

## Sandbox <a id="sandbox"></a>

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sandbox/status` | Sandbox container reachability |
| `POST` | `/sandbox/exec` | Direct exec, **still routed through `executor.RunScopedCommand`** — the gateway scope check applies exactly as it does to an agent-proposed command; this endpoint is not a bypass |

## System <a id="system"></a>

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/system/status` | Aggregated status: Docker, gateway, sandbox, conductor/LLM |
| `GET` | `/system/health-metrics` | Time-series metrics backing the [Health tab](usage-guide.md#tab-health) |
| `GET` | `/system/logs` | Server-sent-events debug log stream |
| `GET` | `/llm/status` | Ollama reachability + resilient-client state |
| `GET` | `/healthz` | Plain container/CLI health probe (outside `/api/v1`) |

---

## WebSockets <a id="websockets"></a>

| Path | Purpose |
|---|---|
| `/ws/terminal` | Bridges to a real interactive shell in the sandbox container — backs the [Bash tab](usage-guide.md#tab-bash) |
| `/ws/events` | Fan-out event stream every other tab subscribes to |

`/ws/events` carries three event `type`s today, each a thin JSON envelope around the relevant record:

| `type` | Fired when | Drives |
|---|---|---|
| `execution.status` | An execution's status changes (`queued` → `running` → `completed`/`failed`/`canceled`/`awaiting_egress_approval`/`awaiting_threat_approval`) — also re-fired periodically as `execution.running` with `elapsed_secs` set while a still-running command is in flight, so a long real command (a full `nikto`, `nmap`) doesn't look indistinguishable from a hang | Live command status and elapsed time in the Agent/Bash tabs |
| `task.status` | An agent task's status changes (running → awaiting_approval → completed/failed/stopped, etc.) | The Agent tab's live task tracker |
| `gateway.status` | A gateway rule or egress mode changes (`health.Aggregator`'s `diffAndPublishRules`, which polls gateway and only emits on an actual diff) | The Gateway tab's live rule table and mode indicator |

Both WebSocket endpoints validate the `Origin` header against localhost before completing the upgrade handshake — a page loaded from any other origin cannot open either socket, even from a browser that already has the UI's own tab open.
