# Yummi Documentation

← [Back to the project README](../README.md) for a quick overview and the fastest path to running Yummi.

This directory is the complete, in-depth documentation set. It's organized around one idea: **the agent is the product** — everything else (the gateway, the sandbox, docker-proxy, the UI) exists to let that agent operate safely. Pages are grouped so you can go as deep as you need without wading through everything.

## Layout

```
docs/
  architecture.md         The 5 runtime components, network topology, the 3 core data flows
  security-model.md       Fail-closed network layer + content-layer threat scanning + audit chain
  installation.md         OS/architecture-specific setup, the cgo/Go-version/Node gotchas, troubleshooting
  usage-guide.md           The 6 UI tabs, approval flows, the CLI and TUI
  development.md          Fast build loops, running a single test, the two-Go-modules trap
  api-reference.md        Every REST route and WebSocket event, grouped by domain

  agent/                  The agent AS A SYSTEM — behavior, not file layout
    overview.md            What makes this agent different from a plain LLM loop
    graph-and-nodes.md      The 11-node state machine, stuck detection, checkpointing
    tag-protocol.md         The closed agent↔LLM vocabulary and robust parsing
    memory-and-knowledge.md Targets/findings/tactics/context, structured + semantic retrieval
    skills-and-subagents.md Declarative playbooks, depth-1 concurrent delegation, auto-skill-drafting
    sandbox-execution.md    The one path every command takes to a real shell

  modules/                Code organized BY IMPLEMENTATION STACK — where to find things
    orchestrator.md         Go — the 20 orchestrator packages and the conductor/executor boundary
    gateway.md               Rust — the trust-boundary module map and startup sequence
    cli-and-tui.md           Go (root module) — the yummi binary and its terminal UI
    ui.md                    Node/React — the dashboard's directory structure

  persona/
    persona-and-skills.md   The two independent personas (conductor vs. agent), safety boundaries
```

## Reading paths

**"I just want to run it."** → [Installation](installation.md), then [Usage Guide](usage-guide.md).

**"I want to understand what this actually does before I trust it with real targets."** → [Architecture](architecture.md), then [Security Model](security-model.md).

**"I want to modify or extend the agent's own reasoning."** → [Agent Overview](agent/overview.md) → [Graph & Nodes](agent/graph-and-nodes.md) → [Tag Protocol](agent/tag-protocol.md).

**"I need to find which file owns X."** → [Modules](modules/) — organized by Go/Rust/Node, mirroring the actual module boundaries in the codebase.

**"I'm integrating against the API directly (a custom client, a script).** → [API Reference](api-reference.md).

## A note on how this documentation was written

These pages were written by reading the current source and checking claims against it, rather than carrying forward an earlier design note or a stale roadmap as-is. Where an older internal plan turned out to disagree with what the code actually does today (semantic memory retrieval being described as "planned" when it's implemented; the egress Ask-mode approval round-trip being described as "still open" when it's complete; a "four components" architecture that's actually five), the pages here follow the code, and say so explicitly rather than silently repeating the outdated version. That said, a page like this is a snapshot, not a live query against the repository — a subsequent review of this same documentation found and fixed several of its own errors (a misleading diagram, a wrong dependency order, an inaccurate "config-only" claim about hostname rules, fabricated example test names, and an internally-inconsistent claim about how the content-layer scanner behaves), which is worth knowing before treating anything here as beyond doubt. If you find a page that's drifted from the code as the project evolves, that's a documentation bug worth fixing the same way you'd fix any other bug — not a reason to distrust the rest of the set, but not a reason to skip verifying either.
