[Back to Project README](../README.md) · [Documentation Index](README.md)

# REST & WebSocket API Reference

The operator HTTP and WebSocket interface is available at `http://127.0.0.1:9080` (also port 3000). In Compose, both host ports terminate at UI nginx; the orchestrator binds `0.0.0.0:9080` inside its container and publishes no host port. A native developer process defaults to `127.0.0.1:9080`.

Routes are defined in [routes.go](../orchestrator/internal/api/routes.go). Operator access checks the actual socket peer, local Host header and any supplied Origin; it is a local single-operator boundary without a login/token system. The separate sandbox policy listener at `172.20.0.3:9081` exposes only policy reads, not these operator routes. See [Security Model](security-model.md).

---

## Table of Contents

- [Conventions & Error Format](#conventions)
- [Sessions API](#sessions)
- [Targets & Findings API](#targets-findings)
- [Attack Surface Management (ASM) & Assets API](#assets)
- [Program Scope Rules API](#scope-rules)
- [Multi-Format Report Generator API](#reports)
- [Proxy & Traffic Ingestion Bridge API](#proxy)
- [TRACE Observability & Operator API](#trace)
- [Chat & Command Proposals API](#chat)
- [Agent Tasks & Lifecycle API](#agent-tasks)
- [Command Executions API](#executions)
- [Gateway Management API](#gateway)
- [Agent Terminals API](#agent-terminals)
- [Sandbox Direct Exec API](#sandbox)
- [System & Health Diagnostics API](#system)
- [WebSocket Streams & Events](#websockets)

---

## Conventions & Error Format <a id="conventions"></a>

- **Base Path**: All REST routes are under `/api/v1` (except WebSocket upgrade paths and `/healthz`).
- **Error Standard**: Responses follow **RFC 7807** problem-detail JSON (`Content-Type: application/problem+json`):
  ```json
  {
    "type": "about:blank",
    "title": "Invalid session ID format",
    "status": 400,
    "detail": "Session IDs must match ^[a-zA-Z0-9-]{1,64}$"
  }
  ```
- **Validation**: Path variables `{sessionID}`, `{id}` are strictly validated before store access.
- **CORS & Origin Security**: Restricted strictly to loopback origins (`localhost`, `127.0.0.1`, `::1`).

---

## Sessions API <a id="sessions"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions` | None | List all sessions. |
| `POST` | `/api/v1/sessions` | `{"name": "string"}` | Create a new engagement session. |
| `GET` | `/api/v1/sessions/{id}` | None | Retrieve session details. |
| `GET` | `/api/v1/sessions/{id}/messages` | None | Retrieve chat history for the session. |
| `PATCH` | `/api/v1/sessions/{id}` | `{"name": "string"}` | Update session metadata. |
| `DELETE` | `/api/v1/sessions/{id}` | None | Delete session and associated transient state. |

---

## Targets & Findings API <a id="targets-findings"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/targets` | None | List declared and discovered targets for the session. |
| `POST` | `/api/v1/sessions/{sessionID}/targets` | `{"value": "198.51.100.10", "in_scope": true}` | Declare a new in-scope target. |
| `GET` | `/api/v1/sessions/{sessionID}/findings` | None | List structured security findings. |
| `POST` | `/api/v1/sessions/{sessionID}/findings` | `{"title": "...", "severity": "high", "description": "...", "evidence": "..."}` | Add a manual finding. |

---

## Attack Surface Management (ASM) & Assets API <a id="assets"></a>

| Method | Endpoint | Query / Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/assets` | `?format=tree` or `?format=flat` (default) | Retrieve hierarchical asset tree or flat inventory list. |
| `POST` | `/api/v1/sessions/{sessionID}/assets` | `{"parent_id": "...", "type": "endpoint", "value": "GET /v1/users", "metadata": {...}}` | Record a discovered asset node. |

### Asset Types:

- `domain`, `subdomain`, `ip`, `port`, `endpoint`, `parameter`, `technology`, `cloud_bucket`, `secret`.

---

## Program Scope Rules API <a id="scope-rules"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/scope-rules` | None | List configured in-scope and out-of-scope program rules. |
| `POST` | `/api/v1/sessions/{sessionID}/scope-rules` | `{"pattern": "*.example.com", "kind": "include", "note": "..."}` or `{"rules": [...]}` | Add one or more program-scope wildcard rules (`include` or `exclude`). |
| `DELETE` | `/api/v1/sessions/{sessionID}/scope-rules/{ruleID}` | None | Delete a specific program-scope rule by ID. |
| `POST` | `/api/v1/sessions/{sessionID}/scope-rules/classify` | `{"hostname": "api.example.com"}` or `{"hostnames": ["api.example.com"]}` | Classify names against program scope; returns an object keyed by submitted hostname, with a scope verdict for each. |

---

## Multi-Format Report Generator API <a id="reports"></a>

| Method | Endpoint | Query Parameters | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/report` | `?format=hackerone` (default)<br>`?format=bugcrowd`<br>`?format=executive`<br>`?format=json` | Generates full penetration test and bug bounty reports from session findings and assets. |

Program-scope rules are knowledge/report bookkeeping; they do not grant or block network access. Gateway policy is the execution-time network authority.

### Export Formats:

- **`hackerone`**: HackerOne Markdown with available CVSS/CWE metadata, PoC steps and impact; missing metadata is not an independently calculated assessment.
- **`bugcrowd`**: Bugcrowd VRT Markdown format with priority tiers (P1 to P5).
- **`executive`**: High-level management summary, asset inventory, and severity risk matrix.
- **`json`**: Structured JSON export for SIEM, SOAR, or automated pipelines.

---

## Proxy & Traffic Ingestion Bridge API <a id="proxy"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/proxy/status` | None | Returns static proxy-integration metadata, including a `ca_installed` flag; it does not inspect/install a real CA or launch an interception proxy. |
| `POST` | `/api/v1/proxy/inject` | `{"session_id": "...", "method": "GET", "url": "https://api.target.com/users?id=1", "status_code": 200, ...}` | Accepts a submitted flow and extracts URL/host/endpoint/parameter assets; raw request/response bodies are not stored as a traffic archive. |

---

## TRACE Observability & Operator API <a id="trace"></a>

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/activity` | JSON list of recorded activity events (`?limit=N`), not a streaming response. |
| `GET` | `/api/v1/sessions/{sessionID}/context` | List persistent session memory entries. |
| `DELETE` | `/api/v1/sessions/{sessionID}/context/{key}` | Explicitly remove a persistent memory entry and embedding. |
| `GET` | `/api/v1/tactics` | List cross-session target-scrubbed procedural tactics. |
| `GET` | `/api/v1/skills` | List active skills and their capability contracts. |
| `GET` | `/api/v1/skills/usage` | Retrieve skill usage statistics. |
| `POST` | `/api/v1/skills/propose` | Submit a completed manual trajectory for curator review (see `handlers_skills.go`). |
| `GET` | `/api/v1/skills/pending` | List curator-drafted skills awaiting operator approval. |
| `POST` | `/api/v1/skills/pending/{id}/approve` | Approve and atomically persist a drafted skill. |
| `POST` | `/api/v1/skills/pending/{id}/reject` | Mark a drafted skill candidate rejected. |
| `GET` | `/api/v1/operator/briefing` | Surface relevant skills, tactics, and findings for the operator. |
| `GET` | `/api/v1/agent/roles` | List available specialist sub-agent roles. |
| `GET` | `/api/v1/agent/capabilities` | Return agent runtime engine and tool capabilities. |
| `GET` | `/api/v1/agent/delegations` | List active and historical specialist sub-agent runs. |
| `POST` | `/api/v1/agent/delegations/{id}/interrupt` | Selectively interrupt one sub-agent run without halting the parent task. |
| `GET` | `/api/v1/sessions/{sessionID}/audit` | Retrieve session event audit log. |

---

## Chat & Command Proposals API <a id="chat"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `POST` | `/api/v1/chat` | `{"message": "...", "session_id": "...", "turn_id": "optional-client-id"}` | Send natural language input; returns command proposals or initiates agent tasks. |
| `POST` | `/api/v1/chat/approve` | `{"proposal_id": "...", "session_id": "..."}` | Approve a single-command proposal for immediate execution. |

---

## Agent Tasks & Lifecycle API <a id="agent-tasks"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/agent/mode` | None | Get agent approval mode (`auto` / `ask_permission`). |
| `POST` | `/api/v1/agent/mode` | `{"mode": "auto"}` | Set runner approval mode for subsequent tasks; active tasks retain their own snapshot. |
| `POST` | `/api/v1/tasks` | `{"goal": "...", "session_id": "..."}` | Plan a multi-step task (does not start execution). |
| `GET` | `/api/v1/tasks/{id}` | None | Get task execution state and step list. |
| `GET` | `/api/v1/sessions/{sessionID}/tasks` | None | List tasks associated with a session. |
| `POST` | `/api/v1/tasks/{id}/start` | None | Begin execution of a planned task. |
| `POST` | `/api/v1/tasks/{id}/stop` | None | Stop execution of an active task. |
| `POST` | `/api/v1/tasks/{id}/approve` | `{"proposal_id": "pending-proposal-id", "approved": true, "reason": "..."}` | Resolve an approval gate on a pending step. |
| `POST` | `/api/v1/tasks/{id}/auto-approve` | `{"enable": true}` | Toggle low/medium-risk auto-approval for one task; high/critical and destructive floors still gate. |

---

## Command Executions API <a id="executions"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/executions` | List command executions for a session. |
| `GET` | `/api/v1/executions/{id}` | Get execution details, output buffers, exit code, and command rationale. |
| `POST` | `/api/v1/executions/{id}/cancel` | Cancel a running command execution. |
| `POST` | `/api/v1/executions/{id}/resolve-egress` | Resolve an egress hold (`{"approved": true}`); approval adds the required rule and requeues execution, denial fails it. |
| `POST` | `/api/v1/executions/{id}/resolve-threat` | Resolve a threat-scan content hold (`{"approved": true}`). |

---

## Gateway Management API <a id="gateway"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/gateway/status` | Retrieve gateway reachability, uptime, and security state. |
| `GET` | `/api/v1/gateway/rules` | List gateway policy rules; inspect gateway enforcement status separately for kernel synchronization. |
| `POST` | `/api/v1/gateway/rules` | Add temporary rule (`{"network": "198.51.100.10/32", "ttl_secs": 3600, "reason": "..."}`). |
| `DELETE` | `/api/v1/gateway/rules/{id}` | Delete a temporary rule by ID (configured rules cannot be removed through this route). |
| `GET` | `/api/v1/gateway/audit` | Query tamper-evident SHA-256 audit chain (`?limit=N`). |
| `GET` | `/api/v1/gateway/fingerprint` | Retrieve TTL normalization and MAC randomization state. |
| `GET` | `/api/v1/gateway/egress-mode` | Get current egress mode (`block_all`, `block_all_except_rules`, `allow_all`, `ask_every_time`; accepts legacy aliases). |
| `POST` | `/api/v1/gateway/egress-mode` | Set egress mode (`{"mode": "allow_all"}`; resets to `block_all` on daemon restart). |
| `GET` | `/api/v1/gateway/hostname-policy` | Retrieve hostname allow/block glob rules. |
| `POST` | `/api/v1/gateway/hostname-policy` | Add pattern to hostname policy (`{"list": "block", "pattern": "*.internal"}`). |
| `DELETE` | `/api/v1/gateway/hostname-policy` | Remove a runtime hostname pattern using the same `{"list": "block", "pattern": "*.internal"}` JSON shape. |

---

## Agent Terminals API <a id="agent-terminals"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/agent-terminals` | List active agent-managed persistent tmux terminal sessions. |
| `GET` | `/api/v1/agent-terminals/{id}` | Read recent output buffer, execution status, and metadata for an agent terminal session. |
| `DELETE` | `/api/v1/agent-terminals/{id}` | Close an agent terminal session; this is the operator write operation for this watch view. |

---

## Sandbox Direct Exec API <a id="sandbox"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/sandbox/status` | Check Kali sandbox container health and reachability. |
| `POST` | `/api/v1/sandbox/exec` | Synchronous operator exec (`{"command": "nmap -sV 198.51.100.10", "session_id": "..."}`). Uses `executor.RunScopedCommand` and the gateway check; does not create an `execution.Store` record or a task command-approval gate. |
| `POST` | `/api/v1/sandbox/upload` | Upload multipart field `file` and optional `session_id` into sandbox `/workspace`; filename is sanitized. |

---

## System & Health Diagnostics API <a id="system"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/system/status` | Aggregated health status across Docker, Gateway, Sandbox, and LLM. |
| `GET` | `/api/v1/system/health-metrics` | Ring-buffer latency and error rate metrics. |
| `GET` | `/api/v1/system/web-search` | Get sandbox web research state (`{"enabled": bool}`). |
| `POST` | `/api/v1/system/web-search` | Toggle sandbox web research state (`{"enabled": bool}`). Fails closed on error. |
| `GET` | `/api/v1/system/logs` | Server-Sent Events (SSE) stream of internal debug logs. |
| `GET` | `/api/v1/llm/status` | Ollama connection and resilient circuit-breaker state. |
| `GET` | `/healthz` | Liveness probe; subject to operator peer/Host/Origin checks and not proof that every dependency is ready. |

---

## WebSocket Streams & Events <a id="websockets"></a>

| Path | Purpose |
|---|---|
| `/ws/terminal` | Interactive PTY shell bridge to the sandbox container (Bash tab). |
| `/ws/desktop` | Proxy to noVNC / websockify graphical desktop stream (Desktop tab). |
| `/ws/events` | High-throughput, typed JSON event bus for UI state synchronization with monotonic replay support. |

### Event envelope and replay

The connection accepts `session_id`, `epoch`, and `after` query parameters. It starts with `control.hello`, whose `data` contains `schema_version`, `epoch`, `latest_sequence`, and `resync_required`. If resync is required, refresh authoritative REST state before continuing.

Subsequent messages use this envelope (see [event_hub.go](../orchestrator/internal/api/event_hub.go)):

```json
{
  "schema_version": 1,
  "epoch": "process-uuid",
  "sequence": 42,
  "type": "execution.status",
  "event": "execution.running",
  "timestamp": 1792184400,
  "session_id": "session-id",
  "data": {}
}
```

| `type` | `event` / payload |
|---|---|
| `execution.status` | Execution lifecycle event such as `execution.running` or `execution.completed`; `data` is the execution record. Running heartbeats carry elapsed time. |
| `task.status` | Task lifecycle event and task snapshot. |
| `agent.activity` | Typed activity record; internal diagnostic detail is excluded from JSON. |
| `gateway.status` | Gateway rule/status event with the current rules. |
| `skill.status` | Curator skill lifecycle event and candidate snapshot. |
| `chat.stream` | `chat.delta` / `chat.done`; live-only model text chunks bound to `turn_id`. |
| `session.changed` | Session creation/update/deletion published by session handlers. |

Execution statuses are `queued`, `running`, `awaiting_egress_approval`, `awaiting_threat_approval`, `completed`, `failed`, and `canceled`. Command approval belongs to task/proposal state, not an execution status named `awaiting_approval`.

Queued shell executions use `RunScopedCommandStream` and `Bridge.ExecStream` to emit stdout/stderr chunks as `type: "execution.status", event: "execution.output"`. The record carries ephemeral `output_chunk` and `output_source` fields; final output is retained separately. Running heartbeats carry elapsed time. Direct synchronous `/sandbox/exec` calls and MCP tool calls have different output paths; do not assume every tool streams. There are no top-level `execution.heartbeat`, `skills.pending`, or `health.tick` message types. Health metrics use REST; debug logs use SSE.

Replay retains a bounded process-local ring (512 lifecycle envelopes), not durable history. Chat/output chunks are live-only. A new epoch, an evicted cursor or a slow-subscriber disconnect requires REST reconciliation. Sequence numbers are global and may have gaps for a session-filtered subscriber.
