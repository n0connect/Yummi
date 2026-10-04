<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">A privacy-first, fail-closed AI pentest conductor and autonomous Red Team platform.</p>

<p align="center">
  <a href="https://github.com/n0connect/yummi/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/n0connect/yummi/actions/workflows/ci.yml/badge.svg?branch=main" /></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-brightgreen?style=flat-square" /></a>
  <a href="https://github.com/n0connect/yummi/releases"><img alt="Version" src="https://img.shields.io/github/v/release/n0connect/yummi?style=flat-square&color=brightgreen&label=version" /></a>
  <a href="docs/installation.md"><img alt="Go" src="https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square" /></a>
  <a href="gateway/Cargo.toml"><img alt="Rust" src="https://img.shields.io/badge/Rust-stable-DEA584?style=flat-square" /></a>
  <a href="ui/package.json"><img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?style=flat-square" /></a>
</p>

<p align="center">
  <b><a href="docs/README.md">Full Documentation</a></b> ·
  <a href="docs/installation.md">Installation</a> ·
  <a href="docs/architecture.md">Architecture</a> ·
  <a href="docs/security-model.md">Security Model</a> ·
  <a href="docs/usage-guide.md">Usage Guide</a> ·
  <a href="docs/api-reference.md">API Reference</a>
</p>

<p align="center">
Yummi translates natural language into verified security workflows while enforcing strict operator authority and fail-closed network control. An autonomous agent drives a hardened Kali Linux workbench equipped with 54 native pentest tools, 99 battle-tested offensive skills, and 52 specialist sub-agents, while an independent Rust gateway enforces packet-level policy outside the model's reach.
</p>

---

## Key Capabilities

### Security Boundary & Traffic Governance
- **Fail-Closed Rust Gateway** — Packet filtering is enforced by an independent Rust daemon (`yummi-gateway`) via Linux kernel `nftables` rules. Default-deny policies install at startup and persist even if the daemon terminates. The AI agent never manages firewall rules.
- **Runtime 3-Way Egress Mode** — Operator controls egress on the fly: `block` (fail-closed, zero outbound traffic), `allow` (blanket external connectivity), or `ask` (destination-by-destination interactive operator approval).
- **Tamper-Evident Audit Chain** — Every routing decision, temporary rule, and egress evaluation is cryptographically chained using SHA-256 in a local SQLite WAL journal.

### Offensive Arsenal & Pwnbox Environment
- **Unified Kali Sandbox (`yummi-pwnbox`)** — Single hardened container running as an unprivileged user (`UID 1000`). Shares operator Bash (`tmux`), graphical desktop (`noVNC`/XFCE), and agent execution.
- **54 Native MCP Tools** — Four-tier tool strategy: 6 typed core tools (Nmap, WhatWeb, cURL, Gobuster, ffuf, Nikto), 39 cataloged offensive utilities, 2 Go recon pipelines, and 7 persistent tmux terminal tools.
- **Persistent Tmux Terminal Verbs** — Full session lifecycle (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`) for long-running listeners, background tunnels, reverse shells, and interactive prompts.
- **Active Directory & Network Pivoting** — Pre-staged Kerberos (`krb5-user`, `kerbrute`), Impacket, NetExec, Nishang, Metasploit Framework, OpenVPN lab profiles (`/dev/net/tun`), and an automatic reverse shell generator (`revshell`).

### Autonomous Reasoning & Specialized Agents
- **Dual Agent Backends** — Choose between a deterministic 11-node Graph State Machine (optimized for local 8B models with closed tag protocol) or CloudWeGo's Eino ADK (native tool-calling for 14B+ models).
- **99 Offensive Skills & 52 Specialist Sub-Agents** — Declarative offensive methodologies and bounded depth-1 specialist roles spanning Active Directory, Web/APIs, Cloud/K8s, Binary RE, Hardware/RF, and Adversary Emulation.
- **Active Exploit Verification Oracles** — Findings require deterministic proof (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`) before acceptance, eliminating hallucinated vulnerabilities.

### Operator Surface & Attack Surface Management
- **Single Web-Managed Workspace** — Clean React 18 dashboard on port 3000 featuring AI Chat with live rationale, tmux Web Terminal, full XFCE Desktop, Trace timeline, Gateway manager, and Health telemetry.
- **Attack Surface Management (ASM)** — Hierarchical asset graph tracking domains, subdomains, IPs, ports, endpoints, query parameters, secrets, and cloud storage buckets.
- **Proxy Ingestion & Multi-Format Reporting** — Live HTTP stream ingestion from Burp Suite, Caido, or Mitmproxy, with one-click report generation in HackerOne Markdown, Bugcrowd VRT, Executive Summary, or structured JSON.

---

## Architecture Topology

```text
                        +----------------------+
       Operator ------->|   ui (React/nginx)   |  :3000
                        +----------+-----------+
                                   | /api, /ws (proxied)
                        +----------v-----------+
       CLI ------------>|  orchestrator (Go)   |  :9080 (127.0.0.1 only)
                        |  conductor + agent   |
                        +----+------------+----+
                             |            |
                 IPC Socket  |            | Docker Exec (via docker-proxy)
        (Length-prefixed)    |            |
                        +----v-------+    |
                        | gateway    |    |
                        | (Rust)     |    |
                        | nftables   |    |
                        +----+-------+    |
                             |            |
               FORWARD Chain |            |
                (Allow/Deny) |            |
                        +----v------------v----+
                        |    sandbox (Kali)    |  No direct internet route;
                        |                      |  egress gated by gateway's
                        |                      |  FORWARD chain.
                        +----------------------+
```

Five runtime components collaborate in a fail-closed architecture:
1. **orchestrator (Go)** — AI conductor, agentic state machine (Graph / Eino), execution store, and REST/WebSocket API.
2. **gateway (Rust)** — Fail-closed `nftables` policy engine, IP/hostname scope evaluator, and tamper-evident SHA-256 audit log.
3. **sandbox (Yummi Pwnbox / Kali Linux)** — Hardened offensive workbench running 54 pentest and terminal MCP tools, Metasploit, Nishang, tmux shell, and XFCE/noVNC desktop.
4. **docker-proxy** — Restricts orchestrator access strictly to container exec/inspect operations; blocks host daemon mutations.
5. **ui (React 18 + Vite)** — Modern, web-only operator surface served via nginx on port 3000.

**[Read the full architecture documentation ->](docs/architecture.md)**

---

## Documentation

Comprehensive guides and technical references live in the [`docs/`](docs/) directory. Start at **[docs/README.md](docs/README.md)**:

- **[Architecture](docs/architecture.md)** — Runtime components, isolation boundaries, and core data flows
- **[Security Model](docs/security-model.md)** — Fail-closed network design, threat scanner, and SHA-256 audit chain
- **[Installation](docs/installation.md)** — Environment prerequisites, container engines, and model setup
- **[Usage Guide](docs/usage-guide.md)** — Target-First launcher, UI tabs, approval flows, and CLI helper
- **[Agent Internals](docs/agent/overview.md)** — Graph state machine, Eino ADK, memory store, and verification oracles
- **[Skills & Sub-Agents](docs/agent/skills-and-subagents.md)** — The 99 built-in playbooks and 52 specialist sub-agents
- **[API Reference](docs/api-reference.md)** — Complete REST endpoints, WebSocket streams, and wire schemas
- **[Development](docs/development.md)** — Multi-module Go workflow, Rust builds, and test harnesses

---

## Quick Start

### 1. Platform Prerequisites
- **Linux (x86_64 / arm64)**: Native Docker Engine (reference production platform).
- **macOS (Apple Silicon)**: Docker with **[OrbStack](https://orbstack.dev/)** strongly recommended for native container bridge performance.
- **Windows 10/11**: **WSL2** with Ubuntu and Docker Desktop WSL2 engine (clone repo inside `~/yummi`, not `/mnt/c/`).

### 2. Bootstrap & Start

```bash
# 1. Install prerequisites (Docker, Go, Rust, Node, Ollama)
make install

# 2. Interactive setup wizard (run once to configure engine, language, and model)
make conf

# 3. Build images and start the full stack
make start
```

Yummi is managed entirely from the web UI at **`http://localhost:3000`**. The root CLI binary (`./bin/yummi`) handles container lifecycle (`--start`, `--stop`, `--status`, `--doctor`) and opens the web UI in an app-style window.

### Configuration Matrix (`make conf`)

Running `make conf` writes your settings to `.yummi.conf` so subsequent commands never require repetitive flags:

| Setting | Options | Description |
|---|---|---|
| `CONTAINER_ENGINE` | `docker`, `podman`, `orbstack` | Selects your container runtime. |
| `YUMMI_NETWORK_MODE` | `secure`, `insecure-bridge` | Fail-closed network mode (`insecure-bridge` enables Docker Desktop VM workarounds). |
| `YUMMI_LANG` | `en`, `tr` | UI display language (compiled into the Vite bundle at build time). |
| `YUMMI_AGENT_BACKEND` | `graph`, `eino` | `graph` for deterministic local models; `eino` for native tool-calling on larger models. |
| `YUMMI_OLLAMA_MODEL` | `gpt-oss:20b`, `gpt-oss:120b`, `qwen3` | Local intent classification and chat model. |
| `YUMMI_EINO_MODEL_API_KEY` | *(Secret input)* | Optional OpenRouter/OpenAI API key for frontier cloud reasoning models. |

---

## Development

```bash
make build   # Builds CLI + orchestrator + gateway binaries AND UI (dist)
make test    # Runs all test suites (Go root, Go orchestrator, Go sandbox-mcp, Rust gateway, UI)
make lint    # Runs golangci-lint across all three Go modules
```

> **Note:** The repository consists of **three separate Go modules** (root CLI, `orchestrator/`, and `sandbox-mcp/`). When running ad-hoc `go test` commands, prefix with the module path (e.g. `cd orchestrator && go test ./...`). See **[docs/development.md](docs/development.md)**.

---

## License

[Apache License 2.0](LICENSE)

<div align="center">
Built with Go · Rust · React · Ollama · OpenRouter · LangGraph
</div>
