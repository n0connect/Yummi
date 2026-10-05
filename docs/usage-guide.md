[Back to Project README](../README.md) · [Documentation Index](README.md)

# Operator Usage Guide

Yummi is managed through a single web UI served on port 3000 (`http://localhost:3000`), backed by the orchestrator REST and WebSocket APIs.

---

## Table of Contents

- [Starting an Engagement Session](#starting-a-session)
- [Target-First Offensive Launcher](#target-launcher)
- [The Eight UI Tabs](#eight-tabs)
  - [1. Agent (Chat & Task Engine)](#tab-agent)
  - [2. Terminals (Agent Tmux Sessions)](#tab-terminals)
  - [3. Trace (ASM, Findings & Observability)](#tab-trace)
  - [4. Bash (Interactive Terminal)](#tab-bash)
  - [5. Desktop (Graphical XFCE/noVNC)](#tab-desktop)
  - [6. Logs (Real-time SSE Debug Stream)](#tab-logs)
  - [7. Gateway (Firewall & Scope Control)](#tab-gateway)
  - [8. Health (Vitals & Telemetry)](#tab-health)
- [Attack Surface Management & Asset Matrix](#asm-usage)
- [Program Scope Rules & Bug Bounty Bookkeeping](#scope-rules-usage)
- [Generating Multi-Format Reports](#reports-usage)
- [Proxy Traffic Bridge (Burp / Caido / Mitmproxy)](#proxy-usage)
- [Two-Stage Exploitation Lab (Apache RCE & SUID Privesc)](#lab-target-usage)
- [Approval Flows & Decision Moments](#approval-flows)
- [CLI Reference](#the-cli)

---

## Starting an Engagement Session <a id="starting-a-session"></a>

Before starting for the first time (or to change your UI language between English and Türkçe, switch models, or configure OpenRouter API keys), run the pre-build configuration wizard:

```bash
make conf
```

Then start the stack:
```bash
make start
```
The interface opens at `http://localhost:3000`. Every engagement is scoped to a **Session** that tracks conversation history, target scope, the hierarchical asset matrix, structured findings, and background tasks. Language (`YUMMI_LANG`), backend selection, and cloud model keys configured in `make conf` persist across sessions in `.yummi.conf`.

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

## The Eight UI Tabs <a id="eight-tabs"></a>

### 1. Agent <a id="tab-agent"></a>
Direct Yummi using natural language or initiate autonomous red-team tasks:
- **Interactive Chat Input**: Bounded auto-expanding multiline input supporting copy-paste and clean file drag-and-drop.
- **Single Command Proposals**: Narrow queries ("run nmap on 10.0.0.5") yield structured proposals with risk ratings.
- **Autonomous Multi-Step Tasks**: Broad goals trigger the state machine graph, generating structured plans and executing steps.
- **Command Rationale Surfacing**: Each command row in the execution timeline surfaces the model's explicit rationale explaining why this command was chosen right now.
- **Persistent Task Summary Cards**: High-visibility terminal completion cards rendering status pills (`✓ COMPLETED`, `✕ FAILED`, `◼ STOPPED`), steps used/budget metrics, executive summary prose, and structured findings with automatic CTF/pentest flag highlighting, 1-click copy interaction, CVE tagging, and direct `Trace ›` navigation. Complete parity across live streaming, page refresh, and session reload.
- **Approval Toggle**:
  - `auto`: Executes non-critical steps autonomously.
  - `ask_permission`: Pauses at every command for explicit operator confirmation.
  - Per-task override: Specific tasks can be granted auto-approval via the UI toggle without switching the global mode.

### 2. Terminals (Agent Tmux Sessions) <a id="tab-terminals"></a>
Live viewer for persistent background tmux sessions opened and managed by the agent:
- Lists all active agent terminals with session IDs, running commands, and process status.
- Watch-only terminal monitor allowing operators to follow live terminal output, inspect interactive CLI sessions (`msfconsole`, `gdb`, `sqlmap`), review background reverse shells, or verify listener logs without interrupting the agent.
- Output matched against suspicious patterns is flagged with security advisory notices.

### 3. Trace <a id="tab-trace"></a>
Observability, intelligence, and asset management cockpit:
- **Activity Timeline**: Sequential events (plans, commands with rationale, results, sub-agent handoffs, findings).
- **Attack Surface Management (ASM) (`AssetTreePanel`)**: Interactive tree indexing domains, subdomains, IPs, ports, endpoints, query parameters, secrets, and cloud buckets.
- **Findings Matrix (`FindingsPanel`)**: Severity-aware, filterable verified vulnerabilities with proof-of-concept evidence.
- **Program Scope Rules (`ScopeRulesPanel`)**: Manage bug-bounty in-scope (`include`) and out-of-scope (`exclude`) wildcard patterns, with a live interactive hostname classifier.
- **Proxy Traffic Bridge (`ProxyBridgePanel`)**: Live ingestion and injection of HTTP flows from Burp Suite, Caido, or Mitmproxy.
- **Report Export (`ReportExportModal`)**: Export findings to HackerOne, Bugcrowd VRT, Executive Summary, or JSON.
- **Tactics**: Target-scrubbed procedural memories applied to the current session.
- **Pending Skills**: Quarantine queue for curator-drafted skills awaiting operator sign-off.

### 4. Bash <a id="tab-bash"></a>
Interactive PTY web terminal directly into the Kali container (`yummi-pwnbox`) over `/ws/terminal`:
- Backed by `tmux` so session state persists across reconnections.
- Operates in the exact same container environment as the AI agent (`UID 1000:1000`).
- Features a tactical ASCII greeting banner, live network IP & VPN indicators (`myip`, `ports`), reverse shell generator (`revshell [PORT]`), and wordlist shortcut variables (`$ROCKYOU`, `$COMMON_TXT`, `$BIG_TXT`, `$USERNAMES`, `$PASSWORDS_TOP`).

### 5. Desktop <a id="tab-desktop"></a>
Graphical Kali XFCE desktop streamed over noVNC (`/ws/desktop`):
- **On-Demand Activation**: Rendered with a click-to-activate eye icon placeholder. The desktop stream stays dormant until activated by the operator, saving significant memory/VRAM and automatically deactivating when switching tabs.
- Pre-installed with **Metasploit Console**, **Burp Suite**, **Wireshark**, and **Firefox**.
- Desktop & home folder shortcuts for **PayloadsAllTheThings**, **SecLists**, **Nishang**, **Wordlists**, **ExploitDB**, and **Workspace**.
- Shares the sandbox filesystem and network namespace with the agent.
- Resizes cleanly with remote display resolution matching the browser viewport.

### 6. Logs <a id="tab-logs"></a>
Live Server-Sent Events (SSE) stream of internal orchestrator debug logs.

### 7. Gateway <a id="tab-gateway"></a>
Operator control panel for the fail-closed network trust boundary:
- **Active Rules**: Live `nftables` FORWARD rules with TTL expiration and manual rule additions/deletions.
- **Audit Chain**: Cryptographic SHA-256 tamper-evident log of all scope and routing decisions.
- **4-Way Egress Mode Switch**: Runtime master switch:
  - `block_all` (Drop All Egress): Complete airgap; drops all outbound traffic unconditionally.
  - `block_all_except_rules` (Strict Scope): Default-drop, permits traffic matching active rules or configured scope.
  - `allow_all` (Blanket Allow): Permits outbound traffic to all public destinations not blocked.
  - `ask_every_time` (Interactive Authorization): Holds each new destination for operator sign-off.
  - *Resets to `block_all` on gateway restart.*

### 8. Health <a id="tab-health"></a>
Real-time telemetry showing Go heap, goroutine count, component health, and LLM round-trip latencies.

### Navigation & Theming
- **Theme Toggle**: Switch between Dark and Light mode directly from the top status bar (persists locally and supports OS system preference).

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

## Program Scope Rules & Bug Bounty Bookkeeping <a id="scope-rules-usage"></a>

Program Scope Rules let you mirror a program's published scope policy into the session. This is knowledge-layer bookkeeping: it auto-classifies discovered assets as in-scope or out-of-scope, and automatically filters out-of-scope findings from generated reports (submitting out-of-scope findings to a bug bounty platform is a policy violation).

```bash
# Add an include (in-scope) rule
curl -X POST http://localhost:9080/api/v1/sessions/<session_id>/scope-rules \
  -H "Content-Type: application/json" \
  -d '{"pattern": "*.example.com", "kind": "include", "note": "Primary web assets"}'

# Add an exclude (out-of-scope) rule
curl -X POST http://localhost:9080/api/v1/sessions/<session_id>/scope-rules \
  -H "Content-Type: application/json" \
  -d '{"pattern": "admin.example.com", "kind": "exclude", "note": "Explicitly out of scope"}'

# Test a target with the live classifier
curl -X POST http://localhost:9080/api/v1/sessions/<session_id>/scope-rules/classify \
  -H "Content-Type: application/json" \
  -d '{"target": "api.example.com"}'
```

In the UI, the **TRACE** tab provides a dedicated **Program Scope Rules** panel where operators can view, add, delete, and live-test scope rules.

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

## Two-Stage Exploitation Lab (Apache RCE & SUID Privesc) <a id="lab-target-usage"></a>

For testing full-autonomy agent workflows against a multi-stage target, Yummi includes a built-in lab environment (`lab-target/`):

```bash
make lab-target-up     # Starts gateway-gated lab target at 198.51.100.10
make lab-target-down   # Stops and removes the lab container
```

The lab container features:
1. **Foothold (Unauthenticated RCE)**: Apache HTTP Server 2.4.49 (CVE-2021-41773 / CVE-2021-42013) path traversal in `/cgi-bin/` with `mod_cgid` enabled, executing arbitrary commands as `www-data`. Flag 1: `/home/user.txt`.
2. **Privilege Escalation**: Misconfigured SUID binary `/usr/local/bin/python3-suid` allowing root escalation. Flag 2: `/root/root.txt`.
3. **Gateway Enforcement**: Runs on RFC 5737 TEST-NET-2 (`198.51.100.0/24`), requiring genuine gateway egress approval or an explicit allow rule just like any real external engagement target.

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
