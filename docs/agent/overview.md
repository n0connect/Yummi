← [Back to README](../../README.md) · [Docs index](../README.md)

# The Agent — Overview

Everything else in this repository — the gateway's nftables enforcement, the sandbox's hardened container, the UI's six tabs — exists to **serve the agent**. The agent is the actual product: it is what turns "enumerate this host and tell me what's exploitable" into a sequence of real decisions, real commands, and a real report. This section of the documentation (`docs/agent/`) is about the agent *as a system* — its reasoning loop, its memory, its delegation model, its relationship with the sandbox — as distinct from [Modules](../modules/) which documents *where the code lives* per implementation language.

## What makes this agent different from a plain LLM loop

Three design choices recur throughout this section, and they're worth naming up front because they explain a lot of otherwise-surprising code:

**1. It's a state machine, not a `while` loop.** The obvious way to build an agent is a loop: ask the model what to do, do it, feed the result back, repeat. Yummi's agent (`orchestrator/internal/agent/graph.go`) is instead an explicit **graph** — named nodes (`step`, `tool`, `consult`, `retry`, `summarize`, `approval`, `finish`, `ask`, `handoff`, `remember`, `threat_hold`) connected by a routing table of named edges. See [The Agent Graph](graph-and-nodes.md) for why this matters in practice: a plain loop's control flow lives implicitly in `if`/`else` branches scattered across a function; a graph's control flow is a *data structure* you can read, test, and reason about independently of the logic inside any one node.

**2. Every message between the agent and the model carries a closed vocabulary tag.** The model never gets raw text back from a tool — it gets `[TOOL_RESPONSE]`, `[TOOL_FAILED]`, `[REPEATED_FAILURE]`, or one of a fixed, documented set of other tags, each with a specific meaning the model has been told about in its own system prompt. See [The Tag Protocol](tag-protocol.md) — this exists specifically because small local models (Yummi's whole design targets models like `qwen3:8b`, not a frontier cloud model) benefit enormously from a rigid, learnable format over free-form prose they might parse inconsistently turn to turn.

**3. The core is deliberately brain-agnostic.** Nothing in the graph, the protocol, or the memory system assumes a *specific* model's quirks — the design target is "any model good enough to follow the tag protocol," with `qwen3` as today's reference, swappable via `YUMMI_OLLAMA_MODEL`/`YUMMI_OLLAMA_TIER` without touching the agent's own code. Where a weak-model-specific accommodation *does* exist (like `finishNode`'s synthesis fallback for a model that never emits `FINISH` on its own — see [Graph & Nodes](graph-and-nodes.md#finish-node)), it's built as graceful degradation the strongest model also benefits from, not a hack that only makes sense for one tier.

## The five pieces, and where to read about each

| Piece | What it is | Page |
|---|---|---|
| **The graph state machine** | 11 nodes, a routing table, stuck/thrash/duplicate detection, checkpointing | [Graph & Nodes](graph-and-nodes.md) |
| **The tag protocol** | The closed agent↔LLM vocabulary and how replies get parsed even from a model that doesn't format perfectly | [Tag Protocol](tag-protocol.md) |
| **Memory & the Knowledge Store** | Targets, findings, target-scrubbed tactics, persistent context, and how they're recalled into future prompts | [Memory & Knowledge](memory-and-knowledge.md) |
| **Skills & sub-agents** | Declarative YAML playbooks, and depth-1 delegation to specialist personas sharing the same Knowledge Store | [Skills & Sub-agents](skills-and-subagents.md) |
| **Sandbox execution** | The scoped scheduled-command path, plus the distinct operator terminal path | [Sandbox Execution](sandbox-execution.md) |

## The one invariant that spans all five

Read together, these five pages describe a structural rule: **the agent package never spawns a command itself.** `agent/exec.go` enqueues into `execution.Store`; `executor.ProcessFunc` drains the store, calls `RunScopedCommand`, obtains gateway verdicts, and only then invokes `sandbox.Bridge.Exec`. See [Sandbox Execution](sandbox-execution.md) and [Security Model](../security-model.md) for the exact boundary and its limits.
