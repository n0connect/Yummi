[Back to Project README](../README.md) · [Documentation Index](README.md)

# Operator Usage Guide

Yummi is managed through a single web UI served on port 3000 (`http://localhost:3000`), backed by the orchestrator REST and WebSocket APIs.

---

## Table of Contents

- [Starting an Engagement Session](#starting-a-session)
- [Target-First Offensive Launcher](#target-launcher)
- [The Seven UI Tabs](#seven-tabs)
  - [1. Agent (Chat & Task Engine)](#tab-agent)
  - [2. Bash (Interactive Terminal)](#tab-bash)
  - [3. Desktop (Graphical XFCE/noVNC)](#tab-desktop)
  - [4. Trace (ASM, Findings & Observability)](#tab-trace)
  - [5. Logs (Real-time SSE Debug Stream)](#tab-logs)
  - [6. Gateway (Firewall & Scope Control)](#tab-gateway)
  - [7. Health (Vitals & Telemetry)](#tab-health)
- [Attack Surface Management & Asset Matrix](#asm-usage)
- [Generating Multi-Format Reports](#reports-usage)
- [Proxy Traffic Bridge (Burp / Caido / Mitmproxy)](#proxy-usage)
- [Approval Flows & Decision Moments](#approval-flows)
- [CLI Reference](#the-cli)

---

## Starting an Engagement Session <a id="starting-a-session"></a>

Start the full stack with:
```bash
make start
```
The interface opens at `http://localhost:3000`. Every engagement is scoped to a **Session** that tracks conversation history, target scope, the hierarchical asset matrix, structured findings, and background tasks.

---

## Target-First Offensive Launcher <a id="target-launcher"></a>

```text
+-----------------------------------------------------------------------------+
|                          TARGET-FIRST LAUNCHER                              |
|                                                                             |
|   Target Input: [ https://api.example.com ]                                 |
|                                                                             |
|   [ Quick Recon ]          [ Web Pentest ]         [ API Audit ]            |
|   - subfinder, amass       - katana, whatweb       - arjun, ffuf            |
|   - dnsx, httpx, alterx    - nuclei, sqlmap        - graphql_cop, jwt_tool  |
+-----------------------------------------------------------------------------+
```

1. **Reconnaissance**: Subdomain discovery (`subfinder`, `amass`), DNS resolution (`dnsx`), URL scraping (`gau`), live web probing (`httpx`).
2. **Web Application Assessment**: Crawler mapping (`katana`), fingerprinting (`whatweb`, `wafw00f`), XSS testing (`dalfox`), SQL injection checks (`sqlmap`), CMS assessment (`wpscan`), template scans (`nuclei`).
3. **API & Logic Testing**: Parameter discovery (`arjun`), GraphQL security (`graphql_cop`), JWT auditing (`jwt_tool`), client-side secret mining (`jsluice`).

---

## The Seven UI Tabs <a id="seven-tabs"></a>

### 1. Agent <a id="tab-agent"></a>
Direct Yummi using natural language or initiate autonomous red-team tasks:
- **Single Command Proposals**: Narrow queries ("run nmap on 10.0.0.5") yield structured proposals with risk ratings.
- **Autonomous Multi-Step Tasks**: Broad goals trigger the state machine graph, generating structured plans and executing steps.
- **Approval Toggle**:
  - `auto`: Executes non-critical steps autonomously.
  - `ask_permission`: Pauses at every command for explicit operator confirmation.

### 2. Bash <a id="tab-bash"></a>
Interactive PTY web terminal directly into the Kali container over `/ws/terminal`:
- Backed by `tmux` so session state persists across reconnections.
- Operates in the exact same container environment as the AI agent (`UID 1000:1000`).

### 3. Desktop <a id="tab-desktop"></a>
Graphical Kali XFCE desktop streamed over noVNC (`/ws/desktop`):
- Pre-installed with **Burp Suite**, **Wireshark**, and **Firefox**.
- Shares the sandbox filesystem and network namespace with the agent.

### 4. Trace <a id="tab-trace"></a>
Observability and intelligence window into the active engagement:
- **Activity Timeline**: Sequential events (plans, commands, results, sub-agent handoffs, findings).
- **Attack Surface Management (ASM)**: Hierarchical domain/endpoint/parameter tree.
- **Findings Matrix**: Verified vulnerabilities with severity ratings and PoC evidence.
- **Tactics**: Target-scrubbed procedural memories applied to the current session.
- **Pending Skills**: Quarantine queue for curator-drafted skills awaiting operator sign-off.

### 5. Logs <a id="tab-logs"></a>
Live Server-Sent Events (SSE) stream of internal orchestrator debug logs.

### 6. Gateway <a id="tab-gateway"></a>
Operator control panel for the fail-closed network trust boundary:
- **Active Rules**: Live `nftables` FORWARD rules with TTL expiration.
- **Audit Chain**: Cryptographic SHA-256 tamper-evident log of all scope decisions.
- **Egress Mode Switch**: 3-way toggle (`block`, `allow`, `ask`).

### 7. Health <a id="tab-health"></a>
Real-time telemetry showing Go heap, goroutine count, and LLM round-trip latencies.

---

## Attack Surface Management & Asset Matrix <a id="asm-usage"></a>

The Knowledge Store automatically constructs a hierarchical asset graph during task execution:

```text
example.com (Domain)
  |-- api.example.com (Subdomain)
  |     |-- 10.0.0.5:443 (IP / Port)
  |     \-- POST /v1/auth/login (Endpoint)
  |           |-- username (Parameter)
  |           \-- password (Parameter)
  \-- s3://backups-example (Cloud Bucket)
```

Operators can query the full asset tree via:
```bash
curl -s "http://localhost:9080/api/v1/sessions/<session_id>/assets?format=tree" | jq .
```

---

## Generating Multi-Format Reports <a id="reports-usage"></a>

Yummi compiles all session intelligence into industry-standard vulnerability reports:

```bash
# 1. HackerOne Markdown Report
curl -s "http://localhost:9080/api/v1/sessions/<session_id>/report?format=hackerone"

# 2. Bugcrowd VRT Markdown Report
curl -s "http://localhost:9080/api/v1/sessions/<session_id>/report?format=bugcrowd"

# 3. Executive Management Summary
curl -s "http://localhost:9080/api/v1/sessions/<session_id>/report?format=executive"

# 4. Machine-Readable JSON Export
curl -s "http://localhost:9080/api/v1/sessions/<session_id>/report?format=json"
```

---

## Proxy Traffic Bridge (Burp / Caido / Mitmproxy) <a id="proxy-usage"></a>

Forward intercepted HTTP traffic directly into Yummi's Knowledge Graph:

```bash
curl -X POST http://localhost:9080/api/v1/proxy/inject \
  -H "Content-Type: application/json" \
  -d '{
    "session_id": "<session_id>",
    "method": "POST",
    "url": "https://api.target.com/v2/tokens?app=mobile",
    "status_code": 200,
    "response_body": "{\"token\": \"eyJ...\"}"
  }'
```
Discovered hosts, endpoints, and query parameters are automatically mapped into the ASM asset tree.

---

## Approval Flows & Decision Moments <a id="approval-flows"></a>

```text
+-----------------------------------------------------------------------------+
|                          OPERATOR APPROVAL FLOWS                            |
|                                                                             |
|   1. Command Risk Gate   -> Gated in `ask_permission` or if risk >= Medium  |
|   2. Egress Approval     -> Gated when Gateway is in `ask` mode             |
|   3. Threat-Scan Hold    -> Gated if command output matches threat patterns |
|   4. Skill Quarantine    -> Gated before drafted skills become active       |
+-----------------------------------------------------------------------------+
```

---

## CLI Reference <a id="the-cli"></a>

```bash
./bin/yummi            # Launches web UI in default browser
./bin/yummi --start     # Starts all docker services
./bin/yummi --stop      # Stops docker services
./bin/yummi --status    # Displays runtime status
./bin/yummi --doctor    # Runs dev & environment diagnostic checks
./bin/yummi --version   # Displays build metadata
```
