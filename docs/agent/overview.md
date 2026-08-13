← [Back to README](../../README.md) · [Docs index](../README.md)

# The Agent — Overview

Everything else in this repository — the gateway's nftables enforcement, the sandbox's hardened container, the UI's six tabs — exists to **serve the agent**. The agent is the actual product: it is what turns "enumerate this host and tell me what's exploitable" into a sequence of real decisions, real commands, and a real report. This section of the documentation (`docs/agent/`) is about the agent *as a system* — its reasoning loop, its memory, its delegation model, its relationship with the sandbox — as distinct from [Modules](../modules/) which documents *where the code lives* per implementation language.

## What makes this agent different from a plain LLM loop

Three design choices recur throughout this section, and they're worth naming up front because they explain a lot of otherwise-surprising code:

**1. It's a state machine, not a `while` loop.** The obvious way to build an agent is a loop: ask the model what to do, do it, feed the result back, repeat. Yummi's agent (`orchestrator/internal/agent/graph.go`) is instead an explicit **graph** — named nodes (`step`, `tool`, `consult`, `retry`, `summarize`, `approval`, `finish`, `ask`, `handoff`, `remember`, `threat_hold`) connected by a routing table of named edges. See [The Agent Graph](graph-and-nodes.md) for why this matters in practice: a plain loop's control flow lives implicitly in `if`/`else` branches scattered across a function; a graph's control flow is a *data structure* you can read, test, and reason about independently of the logic inside any one node.

**2. Every message between the agent and the model carries a closed vocabulary tag.** The model never gets raw text back from a tool — it gets `[TOOL_RESPONSE]`, `[TOOL_FAILED]`, `[REPEATED_FAILURE]`, or one of a fixed, documented set of other tags, each with a specific meaning the model has been told about in its own system prompt. See [The Tag Protocol](tag-protocol.md) — this exists specifically because small local models (Yummi's whole design targets models like `qwen3:8b`, not a frontier cloud model) benefit enormously from a rigid, learnable format over free-form prose they might parse inconsistently turn to turn.

**3. The core is deliberately brain-agnostic.** Nothing in the graph, the protocol, or the memory system assumes a *specific* model's quirks — the design target is "any model good enough to follow the tag protocol," with `qwen3` as today's reference, swappable via `YUMMI_OLLAMA_MODEL`/`YUMMI_OLLAMA_TIER` without touching the agent's own code. Where a weak-model-specific accommodation *does* exist (like `finishNode`'s synthesis fallback for a model that never emits `FINISH` on its own — see [Graph & Nodes](graph-and-nodes.md#finish-node)), it's built as graceful degradation the strongest model also benefits from, not a hack that only makes sense for one tier.

## Two engines behind one interface: graph vs. Eino <a id="two-engines"></a>

The tag protocol above is the right call for small/local models with unreliable structured output — but a larger local model with genuine native tool-calling support (e.g. `qwen3:14b`, confirmed working) doesn't need it. Rather than bolt that onto the graph engine, `orchestrator/internal/agent/task_runner.go` defines a `TaskRunner` interface (`CreateAndPlan`/`Start`/`Stop`/`Approve`/`ApprovalMode`/`Roles`/`DelegationRuns`/... — the full surface `internal/api` needs) that the graph `Runner` and a second implementation, `internal/agent/eino.EinoRunner` (built on [CloudWeGo's Eino ADK](https://github.com/cloudwego/eino)), both satisfy. `cmd/orchestrator/main.go` picks one at startup via `YUMMI_AGENT_BACKEND=graph|eino` (`make conf`, default `graph`) — `internal/api` never knows which is wired in.

The Eino backend reuses the graph engine's own security primitives directly rather than reimplementing them: its `run_command` tool calls the same `Execer`/`Verify`/`threatscan.Banner`/`Gate`/`conductor.Validator` the graph's `toolNode` uses, so the single exec path, risk-gated approval, and content-layer scan all hold identically regardless of which engine is running. What it does **not** yet have: sub-agent `HANDOFF` delegation, the graph's stuck/duplicate/tool-thrash loop-prevention heuristics (aimed at small-model failure modes this backend's target models are less prone to), and checkpoint/crash-recovery — a restart interrupts an in-flight Eino task the same way it would any other unrecovered task. `internal/security/invariants` locks the Eino package to the same "never imports `os/exec`" invariant as the graph engine.

The Eino backend also exposes a three-tier tool strategy backed by a genuine MCP server running inside the Kali sandbox container (`sandbox-mcp/`, a standalone Go module with zero dependency on the orchestrator, reached via a persistent `docker exec -i` session — see [Sandbox Execution § MCP-backed pentest tools](sandbox-execution.md#mcp)): six individually-typed tools (`nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`) with real per-tool JSON schemas for the highest-value programs; `search_pentest_tools`/`run_pentest_tool` for the other thirteen cataloged tools (`amass`, `hydra`, `sqlmap`, `wpscan`, and more) via MCP's own `tools/list` discovery, so the catalog can keep growing without new Eino code; and `run_command` as the free-text fallback for anything that isn't a cataloged pentest tool at all. The graph backend does not gain any of this — the tag-protocol design targets small models that don't benefit from a second tool-calling surface.

## The five pieces, and where to read about each

| Piece | What it is | Page |
|---|---|---|
| **The graph state machine** | 11 nodes, a routing table, stuck/thrash/duplicate detection, checkpointing | [Graph & Nodes](graph-and-nodes.md) |
| **The tag protocol** | The closed agent↔LLM vocabulary and how replies get parsed even from a model that doesn't format perfectly | [Tag Protocol](tag-protocol.md) |
| **Memory & the Knowledge Store** | Targets, findings, target-scrubbed tactics, persistent context, and how they're recalled into future prompts | [Memory & Knowledge](memory-and-knowledge.md) |
| **Skills & sub-agents** | Declarative YAML playbooks, and depth-1 delegation to specialist personas sharing the same Knowledge Store | [Skills & Sub-agents](skills-and-subagents.md) |
| **Sandbox execution** | The scoped scheduled-command path, plus the distinct operator terminal path | [Sandbox Execution](sandbox-execution.md) |
| **Quality & evaluation** | Deterministic persona boundaries, server-side finding verdicts, and assertion-bearing live scenarios | [Quality & Evaluation](quality-and-evaluation.md) |

## The one execution invariant that spans the runtime pieces

Read together, these pages describe a structural rule: **the agent package never spawns a command itself.** `agent/exec.go` enqueues into `execution.Store`; `executor.ProcessFunc` drains the store, calls `RunScopedCommand`, obtains gateway verdicts, and only then invokes `sandbox.Bridge.Exec`. See [Sandbox Execution](sandbox-execution.md) and [Security Model](../security-model.md) for the exact boundary and its limits.
