[Back to Project README](../README.md) · [Documentation Index](README.md)

# REST & WebSocket API Reference

The full HTTP REST and WebSocket interface exposed by the `orchestrator` service (`http://127.0.0.1:9080`). All endpoints are bound strictly to localhost by default.

---

## Table of Contents

- [Conventions & Error Format](#conventions)
- [Sessions API](#sessions)
- [Targets & Findings API](#targets-findings)
- [Attack Surface Management (ASM) & Assets API](#assets)
- [Multi-Format Report Generator API](#reports)
- [Proxy & Traffic Ingestion Bridge API](#proxy)
- [TRACE Observability & Operator API](#trace)
- [Chat & Command Proposals API](#chat)
- [Agent Tasks & Lifecycle API](#agent-tasks)
- [Command Executions API](#executions)
- [Gateway Management API](#gateway)
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
| `POST` | `/api/v1/sessions/{sessionID}/targets` | `{"value": "10.0.0.5", "in_scope": true}` | Declare a new in-scope target. |
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

## Multi-Format Report Generator API <a id="reports"></a>

| Method | Endpoint | Query Parameters | Description |
|---|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/report` | `?format=hackerone` (default)<br>`?format=bugcrowd`<br>`?format=executive`<br>`?format=json` | Generates full penetration test and bug bounty reports from session findings and assets. |

### Export Formats:
- **`hackerone`**: HackerOne Markdown with CVSS v3.1 calculation, CWE categorization, PoC steps, and impact.
- **`bugcrowd`**: Bugcrowd VRT Markdown format with priority tiers (P1 to P5).
- **`executive`**: High-level management summary, asset inventory, and severity risk matrix.
- **`json`**: Structured JSON export for SIEM, SOAR, or automated pipelines.

---

## Proxy & Traffic Ingestion Bridge API <a id="proxy"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/proxy/status` | None | Returns operational status, CA certificate readiness, and supported proxy tools. |
| `POST` | `/api/v1/proxy/inject` | `{"session_id": "...", "method": "GET", "url": "https://api.target.com/users?id=1", "status_code": 200, ...}` | Ingests raw HTTP flows from Burp Suite, Caido, or Mitmproxy into the ASM knowledge graph. |

---

## TRACE Observability & Operator API <a id="trace"></a>

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/activity` | Stream of activity trace events (`?limit=N`). |
| `GET` | `/api/v1/sessions/{sessionID}/context` | List persistent session memory entries. |
| `DELETE` | `/api/v1/sessions/{sessionID}/context/{key}` | Explicitly remove a persistent memory entry and embedding. |
| `GET` | `/api/v1/tactics` | List cross-session target-scrubbed procedural tactics. |
| `GET` | `/api/v1/skills` | List all active red team skills. |
| `GET` | `/api/v1/skills/pending` | List curator-drafted skills awaiting operator approval. |
| `POST` | `/api/v1/skills/pending/{id}/approve` | Approve and atomically persist a drafted skill. |
| `POST` | `/api/v1/skills/pending/{id}/reject` | Reject and purge a drafted skill candidate. |
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
| `POST` | `/api/v1/chat` | `{"message": "...", "session_id": "..."}` | Send natural language input; returns command proposals or initiates agent tasks. |
| `POST` | `/api/v1/chat/approve` | `{"proposal_id": "...", "session_id": "..."}` | Approve a single-command proposal for immediate execution. |

---

## Agent Tasks & Lifecycle API <a id="agent-tasks"></a>

| Method | Endpoint | Request Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/agent/mode` | None | Get agent approval mode (`auto` / `ask_permission`). |
| `POST` | `/api/v1/agent/mode` | `{"mode": "auto"}` | Set global agent approval mode. |
| `POST` | `/api/v1/tasks` | `{"goal": "...", "session_id": "..."}` | Plan a multi-step task (does not start execution). |
| `GET` | `/api/v1/tasks/{id}` | None | Get task execution state and step list. |
| `GET` | `/api/v1/sessions/{sessionID}/tasks` | None | List tasks associated with a session. |
| `POST` | `/api/v1/tasks/{id}/start` | None | Begin execution of a planned task. |
| `POST` | `/api/v1/tasks/{id}/stop` | None | Stop execution of an active task. |
| `POST` | `/api/v1/tasks/{id}/approve` | `{"verdict": "approved"}` | Resolve an approval gate on a pending step. |
| `POST` | `/api/v1/tasks/{id}/auto-approve` | `{"auto_approve": true}` | Toggle auto-approval for a specific task. |

---

## Command Executions API <a id="executions"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/sessions/{sessionID}/executions` | List command executions for a session. |
| `GET` | `/api/v1/executions/{id}` | Get execution details and output buffers. |
| `POST` | `/api/v1/executions/{id}/cancel` | Cancel a running command execution. |
| `POST` | `/api/v1/executions/{id}/resolve-egress` | Resolve an Ask-mode network egress hold. |
| `POST` | `/api/v1/executions/{id}/resolve-threat` | Resolve a threat-scan content hold (`{"approved": true}`). |

---

## Gateway Management API <a id="gateway"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/gateway/status` | Retrieve gateway reachability, uptime, and security state. |
| `GET` | `/api/v1/gateway/rules` | List active `nftables` allow/block rules. |
| `POST` | `/api/v1/gateway/rules` | Add temporary firewall rule (`{"network": "10.0.0.5/32", "ttl_secs": 3600, "reason": "..."}`). |
| `DELETE` | `/api/v1/gateway/rules/{id}` | Manually delete an active firewall rule. |
| `GET` | `/api/v1/gateway/audit` | Query tamper-evident SHA-256 audit chain (`?limit=N`). |
| `GET` | `/api/v1/gateway/fingerprint` | Retrieve TTL normalization and MAC randomization state. |
| `GET` | `/api/v1/gateway/egress-mode` | Get current egress mode (`block`, `allow`, `ask`). |
| `POST` | `/api/v1/gateway/egress-mode` | Set egress mode (resets to `block` on daemon restart). |
| `GET` | `/api/v1/gateway/hostname-policy` | Retrieve hostname allow/block glob rules. |
| `POST` | `/api/v1/gateway/hostname-policy` | Add pattern to hostname policy (`{"list": "block", "pattern": "*.internal"}`). |
| `DELETE` | `/api/v1/gateway/hostname-policy` | Remove pattern from hostname policy. |

---

## Sandbox Direct Exec API <a id="sandbox"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/sandbox/status` | Check Kali sandbox container health and reachability. |
| `POST` | `/api/v1/sandbox/exec` | Direct command exec (`{"command": "nmap -sV 10.0.0.5"}`). **Subject to full gateway scope check.** |

---

## System & Health Diagnostics API <a id="system"></a>

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/system/status` | Aggregated health status across Docker, Gateway, Sandbox, and LLM. |
| `GET` | `/api/v1/system/health-metrics` | Ring-buffer latency and error rate metrics. |
| `GET` | `/api/v1/system/logs` | Server-Sent Events (SSE) stream of internal debug logs. |
| `GET` | `/api/v1/llm/status` | Ollama connection and resilient circuit-breaker state. |
| `GET` | `/healthz` | Unauthenticated readiness and liveness container probe. |

---

## WebSocket Streams & Events <a id="websockets"></a>

| Path | Purpose |
|---|---|
| `/ws/terminal` | Interactive PTY shell bridge to the sandbox container (Bash tab). |
| `/ws/desktop` | Proxy to noVNC / websockify graphical desktop stream (Desktop tab). |
| `/ws/events` | High-throughput, typed JSON event bus for UI state synchronization. |

### Key `/ws/events` Message Types:
- `execution.status`: Live status transitions (`queued`, `running`, `completed`, `failed`, `awaiting_approval`).
- `task.status`: Agent state machine transitions and plan step progress.
- `gateway.status`: Real-time network policy and egress hold notifications.
- `health.tick`: Periodic component health heartbeats.
