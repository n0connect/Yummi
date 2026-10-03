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
Yummi turns natural language into practical security workflows while keeping traffic control and operator authority explicit. An autonomous Red Team agent drives a hardened Kali sandbox (Yummi Pwnbox) with 54 native pentest and terminal tools, Metasploit Framework, Active Directory / Kerberos toolchains, 10 specialized sub-agents, and 22 web/bug bounty playbooks, while a lightweight Rust gateway enforces the documented network policy independently of the model-facing code.
</p>

---

## Key Capabilities

- **Fail-Closed Security Trust Boundary** — Network traffic is strictly gated by a dedicated Rust gateway daemon (`nftables`) with default-deny rules installed at kernel startup. The AI agent never touches firewall rules directly.
- **Yummi Pwnbox: Tactical Red Team Range & Unified Desktop** — Dedicated hostname `yummi-pwnbox`, unified desktop + tmux terminal + full `metasploit-framework` (`msfconsole`, `msfvenom`, meterpreter multi-handlers), Nishang PowerShell post-exploitation framework, Kerbrute, and custom dark tactical theme.
- **Local and Frontier Cloud AI Flexibility** — Run 100% locally with Ollama (`gpt-oss:20b`, `gpt-oss:120b`, `qwen3`), or connect frontier reasoning models (Claude 3.5 Sonnet, GPT-4o, DeepSeek-V3) via OpenRouter or any OpenAI-compatible API using the Eino ADK backend or the LangGraph multi-agent `brain/`.
- **Target-First Web Pentest and Bug Bounty Engine** — One-click asset reconnaissance, subdomain enumeration, live probing, fuzzing, and vulnerability scanning.
- **54 Sandbox-Resident MCP Tools** — 6 individually-typed tools (`nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`), 39 cataloged tools (`httpx_probe`, `dnsx_resolve`, `alterx_mutate`, `gau_fetch`, `katana_crawl`, `dalfox_scan`, `nuclei_scan`, `wpscan_scan`, `wafw00f_scan`, `sqlmap_scan`, `amass_enum`, `subfinder_enum`, `theharvester_scan`, `dnsenum_scan`, `dnsrecon_scan`, `enum4linux_scan`, `onesixtyone_scan`, `snmpwalk_enum`, `dirb_scan`, `hydra_bruteforce`, `medusa_bruteforce`, `checksec_audit`, `radare2_disasm`, `gitleaks_scan`, `masscan_scan`, `sslscan_probe`, `arjun_scan`, `naabu_scan`, `tlsx_scan`, `cloud_enum_scan`, `jwt_tool_inspect`, `subzy_scan`, `jsluice_scan`, `graphql_cop_scan`, `corsy_scan`, `screenshot_triage`, `feroxbuster_scan`, `commix_scan`, `whois_query`), 2 pure-Go utilities (`consolidate_hosts`, `passive_recon_batch`), and 7 persistent terminal session tools (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`).
- **Target Lab VPN Support (`openvpn` + `/dev/net/tun`)** — Instant VPN connectivity for HackTheBox, TryHackMe, Proving Grounds, and client engagement `.ovpn` profiles, seamlessly routed via gateway nftables rules.
- **Active Directory & Kerberos Arsenal** — Native ticket management via `krb5-user` (`kinit`, `klist`, `kdestroy`), `kerbrute` user enumeration and password spraying, alongside `certipy`, `impacket-scripts`, `netexec`, and `nishang`.
- **Built-in Reverse Shell Generator (`revshell`)** — Auto-detects local/VPN IP (`tun0`/`eth0`) and outputs copy-paste reverse shell one-liners across Bash, Python3, Netcat, Socat, PHP, Perl, Ruby, and PowerShell, eliminating third-party web lookups.
- **Wordlist Shortcut Ergonomics** — Standard environment variables (`$ROCKYOU`, `$COMMON_TXT`, `$BIG_TXT`, `$MEDIUM_TXT`, `$USERNAMES`, `$PASSWORDS_TOP`) available to both agent and operator terminal.
- **Persistent Tmux Terminal Sessions** — First-class terminal session lifecycle for interactive programs, long-running listeners, background VPN tunnels, and reverse shells without blocking agent reasoning.
- **Visual Bug-Bounty Triage** — Headless Chrome screenshot capture via `screenshot_triage` alongside HTTP status, technology stack, and page title extraction.
- **Active Exploit Oracles & Verification** — Deterministic proof validation (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`) that verifies vulnerabilities and prevents false positives before reporting.
- **Attack Surface Management (ASM) & Asset Matrix** — Hierarchical graph tracking domains, subdomains, IPs, ports, endpoints, query parameters, secrets, and cloud storage buckets.
- **Program Scope Rules & Report Gate** — Session-level bug bounty in-scope/out-of-scope rule definition (`program_scope_rules`), real-time target classifier, and automated exclusion of out-of-scope findings from final reports.
- **Multi-Format Pentest & Bug Bounty Reporting** — Export findings in HackerOne Markdown, Bugcrowd VRT, Executive Management summaries, and structured JSON.
- **Proxy & Traffic Ingestion Bridge** — Ingest HTTP/S traffic streams from external interception proxies (Burp Suite, Caido, Mitmproxy) directly into the knowledge graph.
- **Unified Operator Workspace & Desktop** — Web-managed interface (port 3000) featuring AI Chat with live execution rationale, tmux-backed Web Terminal (Bash), full XFCE/noVNC Graphical Desktop (Metasploit Console, Burp Suite, Wireshark, Firefox), Trace timeline with ASM tree / scope rules / proxy / report panels, live Gateway policy management, Health monitoring, and Light/Dark themes.
- **Knowledge and Memory System** — Persistent session context, target-scrubbed procedural tactics, structured security findings, 22 specialized attack playbooks, 10 specialized sub-agents, and automated skill drafting with sanitization.
- **Two-Stage Exploitation Lab** — Built-in `lab-target` container (`198.51.100.10`) featuring an unauthenticated Apache path-traversal RCE foothold (CVE-2021-41773) and local SUID privilege escalation (`make lab-target-up`).

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
4. **docker-proxy** — Restricts orchestrator access strictly to container exec/inspect operations; denies host Docker daemon management.
5. **ui (React 18 + Vite)** — Modern, web-only operator surface served via nginx on port 3000.

**[Read the full architecture documentation ->](docs/architecture.md)**

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

---

## Quick Start

### Supported Platforms
- 🐧 **Linux (x86_64 / arm64)**: Native Docker Engine (reference production platform).
- 🍏 **macOS (Apple Silicon)**: Supported with Docker. **[OrbStack](https://orbstack.dev/)** is strongly recommended for seamless container bridge networking.
- 🪟 **Windows 10/11**: Supported via **WSL2** (Ubuntu + Docker Desktop WSL2 engine). Clone and run inside the native WSL2 virtual disk (`~/yummi`), not `/mnt/c/`.

```bash
# 1. Bootstrap environment & dependencies (Docker, Go, Rust, Node, Ollama)
make install

# 2. Interactive setup wizard (CRITICAL: run before building/starting)
#    Configures container engine, network mode, UI language (EN/TR),
#    agent backend (Graph vs Eino), model tiers (Ollama vs OpenRouter API key),
#    and optional LangGraph brain. Persists to gitignored .yummi.conf.
make conf

# 3. Start the full stack (rebuilds images with your config, serves UI on :3000)
make start
```

Yummi is managed entirely from the web UI at `http://localhost:3000`. The root `yummi` binary (`./bin/yummi`) is a helper for container lifecycle, diagnostics (`./bin/yummi --doctor`), and opening the web UI in an app-style browser window.

### Pre-Build Configuration (`make conf`)

Running `make conf` once writes your setup to a gitignored `.yummi.conf` file in the repo root, so subsequent `make build` and `make start` invocations automatically inherit all settings without repeating flags:

- **Container Engine & Network Mode**:
  - `OrbStack` (recommended on macOS — native speed, zero network trade-offs)
  - `Docker Desktop` or `Podman` (enables `insecure-bridge` workaround if needed)
  - `Linux native Docker Engine` (reference production platform)
- **UI Display Language (`YUMMI_LANG`)**:
  - `English (en)` or `Türkçe (tr)`
  - **Important**: UI language is compiled into the Vite frontend bundle at **build time**. To switch languages later, run `make conf` and restart (`make start`).
- **Agent Backend Engine (`YUMMI_AGENT_BACKEND`)**:
  - `Graph`: Deterministic 11-node state machine with closed tag protocol for local/smaller models (`gpt-oss:20b`, `qwen3:8b`).
  - `Eino Local`: CloudWeGo Eino ReAct loop with native tool-calling on larger local models via Ollama.
  - `Eino Cloud (OpenRouter / OpenAI)`: Native tool-calling with frontier models (`anthropic/claude-3.5-sonnet`, `deepseek/deepseek-chat`, `openai/gpt-4o`) via OpenRouter or custom OpenAI-compatible endpoints with hidden API key input (`YUMMI_EINO_MODEL_API_KEY`).
- **Conductor Local Intent Model (`YUMMI_OLLAMA_MODEL`)**:
  - Chat and intent classification always run on local Ollama for zero cost and low latency: `standard` (`gpt-oss:20b`), `expert` (`gpt-oss:120b`), or `custom` tags with optional context size (`YUMMI_OLLAMA_NUM_CTX`).
- **LangGraph Multi-Agent Brain (`brain/`)**:
  - Optional Python multi-agent supervisor topology with OpenRouter/OpenAI API key integration. See [brain/README.md](brain/README.md).

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
