# Yummi Documentation

← [Back to the project README](../README.md) for a quick overview and the fastest path to running Yummi.

This directory is the in-depth documentation set for the agent, operator surfaces, and enforcement components. Pages are grouped by behavior and implementation stack so you can start from a workflow and still reach the owning source code.

## Layout

```
docs/
  architecture.md         The 5 runtime components, network topology, the 3 core data flows
  security-model.md       Fail-closed network layer + content-layer threat scanning + audit chain
  installation.md         OS/architecture-specific setup, the cgo/Go-version/Node gotchas, troubleshooting
  usage-guide.md           The 7 UI tabs, approval flows, the CLI
  development.md          Fast build loops, running a single test, the three-Go-modules trap
  api-reference.md        Every REST route and WebSocket event, grouped by domain

  agent/                  The agent AS A SYSTEM — behavior, not file layout
    overview.md            What makes this agent different from a plain LLM loop
    graph-and-nodes.md      The 11-node state machine, stuck detection, checkpointing
    tag-protocol.md         The closed agent↔LLM vocabulary and robust parsing
    memory-and-knowledge.md Targets/findings/tactics/context, structured + semantic retrieval
    skills-and-subagents.md Declarative playbooks, depth-1 concurrent delegation, auto-skill-drafting
    quality-and-evaluation.md Golden boundary fixtures, objective task metrics, live quality gate, vulnerability-finding benchmark
    sandbox-execution.md    The one path every command takes to a real shell

  modules/                Code organized BY IMPLEMENTATION STACK — where to find things
    orchestrator.md         Go — orchestrator packages and the conductor/executor boundary
    gateway.md               Rust — the trust-boundary module map and startup sequence
    cli.md                   Go (root module) — the yummi binary (Docker lifecycle, doctor, web UI launcher)
    ui.md                    Node/React — the dashboard's directory structure

  persona/
    persona-and-skills.md   The two independent personas (conductor vs. agent), safety boundaries
```

`brain/` (repo root, not under `docs/`) is a separate, optional Python/LangGraph agent surface with its own doc: **[brain/README.md](../brain/README.md)**. It drives the same fail-closed substrate as `graph`/`eino` but through the orchestrator's existing REST API + an MCP server instead of an in-process Go backend — see the [Architecture](architecture.md) page for where it sits, and CLAUDE.md's "brain" section for the source-level summary.

## Reading paths

**"I just want to run it."** → [Installation](installation.md), then [Usage Guide](usage-guide.md).

**"I want to understand what this actually does before I trust it with real targets."** → [Architecture](architecture.md), then [Security Model](security-model.md).

**"I want to modify or extend the agent's own reasoning."** → [Agent Overview](agent/overview.md) → [Graph & Nodes](agent/graph-and-nodes.md) → [Tag Protocol](agent/tag-protocol.md).

**"I need to find which file owns X."** → [Modules](modules/) — organized by Go/Rust/Node, mirroring the actual module boundaries in the codebase.

**"I'm integrating against the API directly (a custom client, a script)."** → [API Reference](api-reference.md).

## Maintenance contract

Documentation is versioned with the code, but source remains authoritative. When a route, wire type, environment default, security boundary, UI tab, Make target, or supported tool changes, update the owning component README and the relevant page here in the same change. Prefer links to source files and real test names over copied schemas or illustrative commands that can silently drift.
