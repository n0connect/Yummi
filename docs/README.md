# Yummi Documentation

[Back to Project README](../README.md)

This directory contains in-depth documentation for the Yummi autonomous pentest platform, covering system architecture, security controls, operator interfaces, and runtime components.

## Directory Layout

```text
docs/
  architecture.md           Five runtime components, network topology, core data flows
  security-model.md         Fail-closed network layer, threat scanning, tamper-evident audit
  installation.md           Platform prerequisites, container engines, model tier setup
  usage-guide.md            Web UI operation (8 tabs), approval workflows, CLI commands
  development.md            Build targets, multi-module testing, development harnesses
  api-reference.md          REST API endpoints and WebSocket event specifications

  agent/                    Agent design and execution behavior
    overview.md             Agent architecture: graph state machine vs. Eino ADK vs. Brain
    browser-automation.md   Modern Chromium automation via Rod, 9 browser tools, accessibility trees
    web-research.md         Controlled sandbox-only web research, web_search/web_fetch, citations
    web-research-qc.md      Browser & research QC test suite, edge-cases and validation results
    execution-provenance.md Structural blacklist protecting runner-owned execution history
    sandbox-confinement.md  Sandbox security boundaries, network isolation, UID 1000, caps
    graph-and-nodes.md      11-node state machine, routing table, active verification oracles
    tag-protocol.md         Closed tag vocabulary for deterministic agent-LLM protocol
    memory-and-knowledge.md Targets, findings, procedural tactics, ASM hierarchical asset graph
    skills-and-subagents.md 99 red team playbooks, 52 specialist subagents, depth-1 delegation
    quality-and-evaluation.md Objective quality metrics and assertion-bearing benchmarks
    sandbox-execution.md    Single scoped execution path and 58 MCP & terminal tools catalog

  modules/                  Component maps organized by implementation stack
    orchestrator.md         Go orchestrator packages, ASM/report/proxy handlers, API routes
    gateway.md              Rust gateway daemon and fail-closed nftables firewall management
    cli.md                  Root Go CLI module (lifecycle and UI launcher)
    ui.md                   React 18 + Vite dashboard structure and state management

  persona/
    persona-and-skills.md   Conductor vs. agent personas and safety boundaries

  ui/
    trace-bash.md           Scrollable Trace cards and persistent operator terminal lifecycle
    ci-verification.md      CI/Docker quality gates and mandatory sandbox test placement
```

The `brain/` directory (located at the repository root) contains an optional Python/LangGraph agent runtime: **[brain/README.md](../brain/README.md)**. It provides a supervisor-specialist multi-agent workflow driving the orchestrator via REST API and MCP.

## Key Subsystems & Features

- **Full-Capability Browser Automation**: Native Chromium automation driven by Rod inside the Kali sandbox with compact accessibility tree snapshots (`@eN` refs including semantic content elements `li`, `td`, `th`, `blockquote`, `code`), multi-tab lifecycle, session cookie isolation, and in-sandbox connection proxy for HTTP/S and WS/WSS enforcing gateway hostname policy. See [Browser Automation](agent/browser-automation.md).
- **Controlled Web Research & Threat Intelligence**: Sandboxed research tools (`web_search`, `web_fetch`) with OpenRouter/Exa cited sources, direct HTTPS plaintext extraction/pagination, RFC 1918/metadata IP rejection, operator-controlled UI toggle button (`btn-web-search`), and prompt-injection defense (`UNTRUSTED_WEB_CONTENT`). See [Web Research](agent/web-research.md) and [Web Research QC](agent/web-research-qc.md).
- **Attack Surface Management (ASM)**: Automatic discovery and maintenance of hierarchical asset graphs (Domain -> Subdomain -> IP -> Port -> Endpoint -> Parameter, Cloud Buckets, Secrets). See [Memory & Knowledge](agent/memory-and-knowledge.md#asm-matrix) and [API Reference](api-reference.md#assets).
- **Program Scope Rules & Report Gate**: Session-level bug-bounty scope directives (`include`/`exclude`), live hostname classifier, and automatic exclusion of out-of-scope findings from generated reports. See [Memory & Knowledge](agent/memory-and-knowledge.md#program-scope) and [API Reference](api-reference.md#scope-rules).
- **Persistent Tmux Terminal Sessions**: First-class background terminal sessions (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`) enabling long-running listeners, VPN connections, and interactive shells. See [Sandbox Execution](agent/sandbox-execution.md#tier-4).
- **Active Exploit Verification**: Finding validation using deterministic oracles (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`). See [Graph & Nodes](agent/graph-and-nodes.md#finding-verification).
- **Multi-Format Report Engine**: One-click generation of professional penetration testing and bug bounty reports in HackerOne Markdown, Bugcrowd VRT, Executive Summary, or structured JSON. See [Usage Guide](usage-guide.md#reports-usage) and [API Reference](api-reference.md#reports).
- **HTTP Proxy Traffic Ingestion**: Live interception and injection of web requests and responses from Burp Suite, OWASP ZAP, or Caido directly into the agent's target scope and parameter catalog. See [API Reference](api-reference.md#proxy).
- **Two-Stage Exploitation Lab**: Deliberately vulnerable target container (`198.51.100.10`) with Apache path-traversal RCE (CVE-2021-41773) and SUID privilege escalation for full-autonomy evaluation (`make lab-target-up`). See [Quality & Evaluation](agent/quality-and-evaluation.md#lab-target).
- **Fail-Closed Execution Sandbox**: Unified Kali Linux container operating behind an isolated Rust gateway enforcing packet-level `nftables` policy rules with tamper-evident SHA-256 hash chains and 58 native MCP tools. See [Security Model](security-model.md) and [Sandbox Execution](agent/sandbox-execution.md).

## Recommended Reading Paths

- **Operator / Pentester Path**:
  [Installation](installation.md) -> [Usage Guide](usage-guide.md) -> [API Reference](api-reference.md)
- **Security & Architecture Review Path**:
  [Architecture](architecture.md) -> [Security Model](security-model.md) -> [Sandbox Execution](agent/sandbox-execution.md)
- **Agent & AI Developer Path**:
  [Agent Overview](agent/overview.md) -> [Graph & Nodes](agent/graph-and-nodes.md) -> [Tag Protocol](agent/tag-protocol.md) -> [Skills & Subagents](agent/skills-and-subagents.md)
- **Codebase Orientation Path**:
  [Development Guide](development.md) -> [Modules Map](modules/) (Go Orchestrator, Rust Gateway, CLI, UI)

## Documentation Maintenance

Documentation is versioned alongside the code. When modifying routes, data schemas, security invariants, UI workflows, or configuration defaults, update the corresponding documentation files in the same pull request. References should link to actual source files and test identifiers where possible.
