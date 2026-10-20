# Yummi Documentation

[Back to Project README](../README.md)

This directory contains in-depth documentation for the Yummi autonomous pentest platform, covering system architecture, security controls, operator interfaces, and runtime components.

## Directory Layout

```text
docs/
  architecture.md           Five runtime components, network topology, core data flows
  security-model.md         Fail-closed network layer, threat scanning, tamper-evident audit
  installation.md           Platform prerequisites, container engines, model tier setup
  configuration.md          Configuration layers, defaults, forwarding and model routing
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
    graph-and-nodes.md      12-node state machine, routing table, active verification oracles
    tag-protocol.md         Closed tag vocabulary for deterministic agent-LLM protocol
    memory-and-knowledge.md Targets, findings, procedural tactics, ASM hierarchical asset graph
    skills-and-subagents.md Declarative playbooks, specialist roles and depth-1 delegation
    quality-and-evaluation.md Objective quality metrics and assertion-bearing benchmarks
    sandbox-execution.md    Scoped shell/MCP execution paths and tool discovery

  modules/                  Component maps organized by implementation stack
    orchestrator.md         Go orchestrator packages, ASM/report/proxy handlers, API routes
    gateway.md              Rust gateway daemon and fail-closed nftables firewall management
    cli.md                  Root Go CLI module (lifecycle and UI launcher)
    ui.md                   React 18 + Vite dashboard structure and state management

  persona/
    persona-and-skills.md   Conductor vs. agent personas and safety boundaries

  releases/                 Historical release notes
  testing/                  Dated test observations
  superpowers/specs/        Historical design proposals, not implementation guarantees
  superpowers/plans/        Historical implementation plans, not completion evidence

  ui/
    trace-bash.md           Scrollable Trace cards and persistent operator terminal lifecycle
    ci-verification.md      CI/Docker quality gates and mandatory sandbox test placement
```

The `brain/` directory (located at the repository root) contains an optional Python/LangGraph agent runtime: **[brain/README.md](../brain/README.md)**. It provides a supervisor-specialist multi-agent workflow driving the orchestrator via REST API and MCP.

## Key Subsystems & Features

- **Full-Capability Browser Automation**: Native Chromium automation driven by Rod inside the Kali sandbox with compact accessibility tree snapshots (`@eN` refs including semantic content elements `li`, `td`, `th`, `blockquote`, `code`), multi-tab lifecycle, session cookie isolation, and in-sandbox connection proxy for HTTP/S and WS/WSS enforcing gateway hostname policy. See [Browser Automation](agent/browser-automation.md).
- **Controlled Web Research & Threat Intelligence**: Sandboxed research tools (`web_search`, `web_fetch`) with OpenRouter/Exa cited sources, direct HTTPS plaintext extraction/pagination, RFC 1918/metadata IP rejection, operator-controlled UI toggle button (`btn-web-search`), and prompt-injection defense (`UNTRUSTED_WEB_CONTENT`). See [Web Research](agent/web-research.md) and [Web Research QC](agent/web-research-qc.md).
- **Attack Surface Management (ASM)**: Automatic discovery and maintenance of hierarchical asset graphs (Domain -> Subdomain -> IP -> Port -> Endpoint -> Parameter, Cloud Buckets, Secrets). See [Memory & Knowledge](agent/memory-and-knowledge.md#asm-matrix) and [API Reference](api-reference.md#assets).
- **Program Scope Rules & Report Gate**: Session-level bug-bounty scope directives (`include`/`exclude`), live hostname classifier, and report warnings for findings outside program scope; this does not filter them out or grant network access. See [Memory & Knowledge](agent/memory-and-knowledge.md#program-scope) and [API Reference](api-reference.md#scope-rules).
- **Persistent Tmux Terminal Sessions**: First-class background terminal sessions (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`) enabling long-running listeners, VPN connections, and interactive shells. See [Sandbox Execution](agent/sandbox-execution.md#tier-4).
- **Active Exploit Verification**: Finding validation helpers using output markers and heuristics (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`). See [Graph & Nodes](agent/graph-and-nodes.md#finding-verification).
- **Multi-Format Report Engine**: One-click generation of professional penetration testing and bug bounty reports in HackerOne Markdown, Bugcrowd VRT, Executive Summary, or structured JSON. See [Usage Guide](usage-guide.md#reports-usage) and [API Reference](api-reference.md#reports).
- **HTTP Proxy Traffic Ingestion**: An HTTP flow ingestion endpoint for external proxy integrations; it extracts URL assets/parameters, and does not itself install a proxy or CA. See [API Reference](api-reference.md#proxy).
- **Two-Stage Exploitation Lab**: Deliberately vulnerable target container (`198.51.100.10`) with Apache path-traversal RCE (CVE-2021-41773) and SUID privilege escalation for full-autonomy evaluation (`make lab-target-up`). See [Quality & Evaluation](agent/quality-and-evaluation.md#lab-target).
- **Fail-Closed Execution Sandbox**: Unified Kali Linux container operating behind an isolated Rust gateway enforcing packet-level `nftables` policy rules with tamper-evident SHA-256 hash chains and sandbox-resident MCP tools. See [Security Model](security-model.md) and [Sandbox Execution](agent/sandbox-execution.md).

## Recommended Reading Paths

- **Operator / Pentester Path**:
  [Installation](installation.md) -> [Configuration](configuration.md) -> [Usage Guide](usage-guide.md) -> [API Reference](api-reference.md)
- **Security & Architecture Review Path**:
  [Architecture](architecture.md) -> [Security Model](security-model.md) -> [Sandbox Execution](agent/sandbox-execution.md)
- **Agent & AI Developer Path**:
  [Agent Overview](agent/overview.md) -> [Graph & Nodes](agent/graph-and-nodes.md) -> [Tag Protocol](agent/tag-protocol.md) -> [Skills & Subagents](agent/skills-and-subagents.md)
- **Codebase Orientation Path**:
  [Development Guide](development.md) -> [Modules Map](modules/) (Go Orchestrator, Rust Gateway, CLI, UI)

## Documentation Maintenance

The root README, undated guides here and per-component READMEs describe the current checkout. [AGENTS.md](../AGENTS.md) holds shared development-agent instructions; [CLAUDE.md](../CLAUDE.md) and [.agent.md](../.agent.md) reference it instead of maintaining independent architecture copies.

Dated files under `superpowers/`, `testing/`, `releases/`, dated `agent/` audits/deployment reports, `CHANGELOG.md`, harness `results.md` files and screenshot captions are historical records. They may describe removed files, older schemas, failed checks, or a live state that has since changed. A plan is not implementation; an old PASS is not verification of this checkout. Preserve those records and use the maintained guides for current commands and behavior.

Use these implementation sources when checking a document:

| Topic | Source of truth |
|---|---|
| Commands and wizard defaults | [Makefile](../Makefile), [configuration example](../.yummi.conf.example), `scripts/` |
| Topology, mounts, ports, image builds | [Compose](../docker-compose.yml), [nginx](../ui/nginx.conf), component Dockerfiles |
| Runtime environment defaults and model wiring | [config.go](../orchestrator/internal/config/config.go), [main.go](../orchestrator/cmd/orchestrator/main.go) |
| REST routes and JSON fields | [routes.go](../orchestrator/internal/api/routes.go) and its handler implementations |
| WebSocket envelope and replay | [event_hub.go](../orchestrator/internal/api/event_hub.go), execution/task stores |
| Gateway policy and kernel enforcement | [policy](../gateway/src/policy/mod.rs), [nftables](../gateway/src/network/nftables.rs); inspect both |
| Graph protocol and nodes | [protocol.go](../orchestrator/internal/agent/protocol.go), [graph.go](../orchestrator/internal/agent/graph.go) |
| Sandbox tools, roles and skills | MCP `tools/list`, `persona/v0.1/subagents/`, `persona/v0.1/skills/` |

When modifying routes, schemas, security boundaries, UI flows or configuration, update the relevant maintained page in the same change. Distinguish source review, isolated testing and live deployment verification. Check local links/anchors, Make targets, JSON request fields and test identifiers against source. Do not replace a missing implementation with a documentation claim.

See the [documentation audit completed 2026-10-20](documentation-audit-2026-10-17.md) for the corrections and remaining source-level mismatches found in this pass.
