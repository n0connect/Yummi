[Back to Project README](../../README.md) · [Docs index](../README.md)

# The Agent — Overview

The agent is the autonomous offensive security engine of Yummi. It translates high-level natural language goals ("audit the authentication API and discover hidden parameters") into structured execution plans, tool dispatches, active vulnerability verification, and comprehensive reports.

---

## Three Agent Engines Behind One Substrate

```text
+-----------------------------------------------------------------------------------+
|                            AGENT ENGINE ARCHITECTURES                             |
|                                                                                   |
|   1. Graph State Machine (Go)        2. Eino ADK Backend (Go)      3. Brain (Py)  |
|   [YUMMI_AGENT_BACKEND=graph]        [YUMMI_AGENT_BACKEND=eino]    [LangGraph]    |
|   - 11-node State Machine            - Native Tool Calling         - Multi-Agent  |
|   - Tag Protocol ([TOOL], [FINISH])  - 39 Typed/Cataloged MCPs     - Supervisor   |
|   - Optimized for local 8B models    - Scaled for 14B+ models      - Frontier LLM |
|        |                                  |                             |         |
|        +----------------------------------+-----------------------------+         |
|                                           |                                       |
|                                           v                                       |
|                  Shared Scoped Executor & Fail-Closed Gateway                     |
+-----------------------------------------------------------------------------------+
```

---

## Core System Architecture Principles

1. **Explicit Graph State Machine**: Control flow lives in an explicit data structure (`graphEdges`) rather than nested procedural loops, providing deterministic routing across step, tool execution, operator consultation, retry, and finish nodes.
2. **Strict Tag Protocol**: For local models, interaction is governed by closed-vocabulary control tags (`[TOOL]`, `[HANDOFF]`, `[REMEMBER]`, `[FINDING]`, `[FINISH]`, `[ASK]`), ensuring reliable parsing turn to turn.
3. **Model-Agnostic Core**: Designed to operate with local models via Ollama (`gpt-oss:20b`, `gpt-oss:120b`, `qwen3`) or cloud reasoning models via OpenRouter without code changes.
4. **Active Verification Exploit Oracles**: Findings require concrete proof (`OracleActivePoC`, `OracleDOMExecution`, `OracleOOBInteraction`, `OracleDifferential`) before being accepted as valid.
5. **Attack Surface Management (ASM)**: Discovered infrastructure is structured into a hierarchical asset graph (Domain -> Subdomain -> IP -> Port -> Endpoint -> Parameter).

---

## Agent Subsystem Documentation Index

| Subsystem | Scope & Contents | Reference |
|---|---|---|
| **Graph & Nodes** | 11 state machine nodes, transition routing table, loop prevention heuristics, and plan checkpointing. | [Graph & Nodes](graph-and-nodes.md) |
| **Tag Protocol** | Closed agent-LLM tag vocabulary, robust token parsers, and prompt construction. | [Tag Protocol](tag-protocol.md) |
| **Memory & Knowledge Store** | Factual targets, findings, target-scrubbed procedural tactics, persistent context, and ASM asset tree. | [Memory & Knowledge](memory-and-knowledge.md) |
| **Skills & Sub-Agents** | 22 declarative offensive playbooks and 10 specialist sub-agents with depth-1 delegation bounding. | [Skills & Sub-agents](skills-and-subagents.md) |
| **Sandbox Execution & MCP** | The 39 sandbox MCP tools across 3 tiers, direct argv execution, and network gating. | [Sandbox Execution](sandbox-execution.md) |
| **Quality & Evaluation** | Assertion-bearing golden test fixtures, persona boundary assertions, and live benchmark harnesses. | [Quality & Evaluation](quality-and-evaluation.md) |

---

## Architectural Invariant: Single Execution Path

`orchestrator/internal/agent` contains **zero** calls to `os/exec` or process spawning functions. Every command requested by any agent engine enqueues into `execution.Store`, passes through `executor.RunScopedCommand`, checks gateway `nftables` policy over IPC, and executes inside the Kali sandbox as an unprivileged user (`UID 1000:1000`).
