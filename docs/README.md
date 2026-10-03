# Yummi Documentation

[Back to Project README](../README.md)

This directory contains in-depth documentation for the Yummi autonomous pentest platform, covering system architecture, security controls, operator interfaces, and runtime components.

## Directory Layout

```text
docs/
  architecture.md           Five runtime components, network topology, core data flows
  security-model.md         Fail-closed network layer, threat scanning, tamper-evident audit
  installation.md           Platform prerequisites, container engines, model tier setup
  usage-guide.md            Web UI operation (7 tabs), approval workflows, CLI commands
  development.md            Build targets, multi-module testing, development harnesses
  api-reference.md          REST API endpoints and WebSocket event specifications

  agent/                    Agent design and execution behavior
    overview.md             Agent architecture: graph state machine vs. Eino ADK vs. Brain
    graph-and-nodes.md      11-node state machine, routing table, active verification oracles
    tag-protocol.md         Closed tag vocabulary for deterministic agent-LLM protocol
    memory-and-knowledge.md Targets, findings, procedural tactics, ASM hierarchical asset graph
    skills-and-subagents.md 22 red team playbooks, 10 specialist subagents, depth-1 delegation
    quality-and-evaluation.md Objective quality metrics and assertion-bearing benchmarks
    sandbox-execution.md    Single scoped execution path and 54 MCP & terminal tools catalog

  modules/                  Component maps organized by implementation stack
    orchestrator.md         Go orchestrator packages, ASM/report/proxy handlers, API routes
    gateway.md              Rust gateway daemon and fail-closed nftables firewall management
    cli.md                  Root Go CLI module (lifecycle and UI launcher)
    ui.md                   React 18 + Vite dashboard structure and state management

  persona/
    persona-and-skills.md   Conductor vs. agent personas and safety boundaries
```

The `brain/` directory (located at the repository root) contains an optional Python/LangGraph agent runtime: **[brain/README.md](../brain/README.md)**. It provides a supervisor-specialist multi-agent workflow driving the orchestrator via REST API and MCP.

## Key Subsystems & Features

- **Attack Surface Management (ASM)**: Automatic discovery and maintenance of hierarchical asset graphs (Domain -> Subdomain -> IP -> Port -> Endpoint -> Parameter, Cloud Buckets, Secrets). See [Memory & Knowledge](agent/memory-and-knowledge.md#asm-matrix) and [API Reference](api-reference.md#assets).
- **Program Scope Rules & Report Gate**: Session-level bug-bounty scope directives (`include`/`exclude`), live hostname classifier, and automatic exclusion of out-of-scope findings from generated reports. See [Memory & Knowledge](agent/memory-and-knowledge.md#program-scope) and [API Reference](api-reference.md#scope-rules).
- **Persistent Tmux Terminal Sessions**: First-class background terminal sessions (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`) enabling long-running listeners, VPN connections, and interactive shells. See [Sandbox Execution](agent/sandbox-execution.md#tier-4).
- **Active Exploit Verification**: Finding validation using deterministic oracles (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`). See [Graph & Nodes](agent/graph-and-nodes.md#finding-verification).
- **Multi-Format Report Engine**: One-click generation of professional penetration testing and bug bounty reports in HackerOne Markdown, Bugcrowd VRT, Executive Summary, or structured JSON. See [Usage Guide](usage-guide.md#reports-usage) and [API Reference](api-reference.md#reports).
- **HTTP Proxy Traffic Ingestion**: Live interception and injection of web requests and responses from Burp Suite, OWASP ZAP, or Caido directly into the agent's target scope and parameter catalog. See [API Reference](api-reference.md#proxy).
- **Two-Stage Exploitation Lab**: Deliberately vulnerable target container (`198.51.100.10`) with Apache path-traversal RCE (CVE-2021-41773) and SUID privilege escalation for full-autonomy evaluation (`make lab-target-up`). See [Quality & Evaluation](agent/quality-and-evaluation.md#lab-target).
- **Fail-Closed Execution Sandbox**: Unified Kali Linux container operating behind an isolated Rust gateway enforcing packet-level `nftables` policy rules with tamper-evident SHA-256 hash chains. See [Security Model](security-model.md) and [Sandbox Execution](agent/sandbox-execution.md).

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
