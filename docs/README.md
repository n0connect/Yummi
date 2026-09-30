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
    overview.md             Agent architecture: graph state machine vs. Eino ADK
    graph-and-nodes.md      11-node state machine, routing table, cycle detection
    tag-protocol.md         Closed tag vocabulary for deterministic agent-LLM protocol
    memory-and-knowledge.md Targets, findings, procedural tactics, context retrieval
    skills-and-subagents.md Playbook selection, depth-1 delegation, skill curation
    quality-and-evaluation. Objective quality metrics and assertion-bearing benchmarks
    sandbox-execution.md    Single scoped execution path and MCP tool catalog

  modules/                  Component maps organized by implementation stack
    orchestrator.md         Go orchestrator packages and subsystem boundaries
    gateway.md              Rust gateway daemon and fail-closed firewall management
    cli.md                  Root Go CLI module (lifecycle and UI launcher)
    ui.md                   React 18 + Vite dashboard structure and state management

  persona/
    persona-and-skills.md   Conductor vs. agent personas and safety boundaries
```

The `brain/` directory (located at the repository root) contains an optional Python/LangGraph agent runtime: **[brain/README.md](../brain/README.md)**. It provides a supervisor-specialist multi-agent workflow driving the orchestrator via REST API and MCP.

## Recommended Reading Paths

- **Quick Start and Setup**: [Installation](installation.md) -> [Usage Guide](usage-guide.md)
- **Security and Architecture Review**: [Architecture](architecture.md) -> [Security Model](security-model.md)
- **Agent Internals and Customization**: [Agent Overview](agent/overview.md) -> [Graph & Nodes](agent/graph-and-nodes.md) -> [Tag Protocol](agent/tag-protocol.md)
- **Codebase Orientation**: [Modules](modules/) (organized by Go, Rust, and TypeScript stacks)
- **API and Tool Integration**: [API Reference](api-reference.md)

## Documentation Maintenance

Documentation is versioned alongside the code. When modifying routes, data schemas, security invariants, UI workflows, or configuration defaults, update the corresponding documentation files in the same pull request. References should link to actual source files and test identifiers where possible.
