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
Yummi turns natural language into practical security workflows while keeping traffic control and operator authority explicit. An autonomous Red Team agent drives a hardened Kali sandbox with 30 native pentest tools and specialized web/bug bounty playbooks, while a lightweight Rust gateway enforces the documented network policy independently of the model-facing code.
</p>

---

## Key Capabilities

- **Fail-Closed Security Trust Boundary** — Network traffic is strictly gated by a dedicated Rust gateway daemon (`nftables`) with default-deny rules installed at kernel startup. The AI agent never touches firewall rules directly.
- **Local and Frontier Cloud AI Flexibility** — Run 100% locally with Ollama (`gpt-oss:20b`, `gpt-oss:120b`, `qwen3`), or connect frontier reasoning models (Claude 3.5 Sonnet, GPT-4o, DeepSeek-V3) via OpenRouter or any OpenAI-compatible API using the Eino ADK backend or the LangGraph multi-agent `brain/`.
- **Target-First Web Pentest and Bug Bounty Engine** — One-click asset reconnaissance, subdomain enumeration, live probing, fuzzing, and vulnerability scanning.
- **30 Sandbox-Resident MCP Tools** — Typed and cataloged tools inside the Kali container: `nmap`, `httpx`, `dnsx`, `alterx`, `gau`, `katana`, `dalfox`, `nuclei`, `wpscan`, `sqlmap`, `amass`, `subfinder`, `theharvester`, `ffuf`, `gobuster`, `nikto`, `hydra`, `checksec`, `radare2`, plus pure-Go concurrency utilities like `passive_recon_batch` and `consolidate_hosts`.
- **Unified Operator Workspace** — Web-managed interface (port 3000) featuring AI Chat, tmux-backed Web Terminal (Bash), full XFCE/noVNC Graphical Desktop (Burp Suite, Wireshark, Firefox), Trace timeline, live Gateway policy management, and Health monitoring.
- **Knowledge and Memory System** — Persistent session context, target-scrubbed procedural tactics, structured security findings, 17 specialized attack playbooks, and automated skill drafting with sanitization.

---

## Documentation

The [`docs/`](docs/) directory provides comprehensive reference material. Start at **[docs/README.md](docs/README.md)** for the complete index and suggested reading paths:

- **[Architecture](docs/architecture.md)** — The 5 runtime components, network topology, and core data flows
- **[Security Model](docs/security-model.md)** — Fail-closed network layer, content-layer threat scanning, and SHA-256 audit chain
- **[Installation](docs/installation.md)** — Setup guide for Linux, macOS, and Windows WSL2, plus model tier configuration
- **[Usage Guide](docs/usage-guide.md)** — Target-First Launcher, the 7 UI tabs, approval flows, and CLI helper
- **[Agent Internals](docs/agent/overview.md)** — State machine graph, Eino tool-calling, memory, skills, and sub-agents
- **[API Reference](docs/api-reference.md)** — Comprehensive REST endpoints, WebSocket streams, and wire schemas
- **[Development](docs/development.md)** — Fast build loops, running single tests, and dev harnesses

## Architecture Overview

*The AI proposes, the gateway disposes.* Agent commands flow through a single scoped execution path.

<p align="center">
  <img src="docs/assets/architecture.svg" alt="Yummi trust-boundary flow diagram: orchestrator executes approved commands into sandbox and requests gateway scope over IPC; gateway nftables rules control external network access." width="720">
</p>

Five runtime components collaborate in a fail-closed architecture:
1. **orchestrator (Go)** — AI conductor, agentic state machine (Graph / Eino), execution store, and REST/WebSocket API.
2. **gateway (Rust)** — Fail-closed `nftables` policy engine, IP/hostname scope evaluator, and tamper-evident SHA-256 audit log.
3. **sandbox (Kali Linux)** — Hardened container running pentest tools, `sandbox-mcp` server, tmux shell, and XFCE/noVNC desktop.
4. **docker-proxy** — Restricts orchestrator access strictly to container exec/inspect operations; denies host Docker daemon management.
5. **ui (React 18 + Vite)** — Modern, web-only operator surface served via nginx on port 3000.

**[Read the full architecture documentation ->](docs/architecture.md)**

## Quick Start

```bash
# 1. Setup dev environment & dependencies (Ollama, Go, Rust, Node, Docker)
make install

# 2. Start the full stack (UI accessible at http://localhost:3000)
make start
```

Yummi is managed entirely from the web UI at `http://localhost:3000`. The root `yummi` binary (`./bin/yummi`) is a helper for container lifecycle, diagnostics (`./bin/yummi --doctor`), and opening the web UI in an app-style browser window.

### Cloud Models and OpenRouter (Optional)

In addition to local Ollama models (`gpt-oss:20b`, `gpt-oss:120b`), frontier models can be configured via OpenRouter:
- **Eino Backend (Go Orchestrator)**: Set `YUMMI_AGENT_BACKEND=eino`, `YUMMI_EINO_PROVIDER=openrouter`, and provide your `YUMMI_EINO_MODEL_API_KEY`.
- **LangGraph Multi-Agent Brain (Python)**: Use `brain/` with `YUMMI_BRAIN_MODEL_API_KEY` for a supervisor and specialist multi-agent topology driving the same fail-closed execution substrate. See [brain/README.md](brain/README.md).

## Development

```bash
make build   # CLI + orchestrator + gateway + UI
make test    # All test suites across Go, Rust, and React
make lint    # golangci-lint on all three Go modules
```

> **Note:** This repository consists of **three separate Go modules** (root CLI, `orchestrator/`, and `sandbox-mcp/`). When running ad-hoc `go test` commands, remember to `cd` into the relevant module directory. See **[docs/development.md](docs/development.md)**.

## License

[Apache License 2.0](LICENSE)

---

<div align="center">

Built with Go · Rust · React · Ollama · OpenRouter · LangGraph

</div>
