[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# The Agent Graph — Nodes & Edges

The agent's task loop is implemented as an explicit graph state machine in `orchestrator/internal/agent/graph.go`, not a procedural `while` loop with nested branching. This page details each state node, the complete transition table, loop prevention heuristics, and verification oracles.

---

## Table of Contents

- [Why a Graph and Not a Loop](#why-a-graph)
- [The 11 State Machine Nodes](#the-nodes)
- [Complete Routing Table](#routing-table)
- [Execution Trace of a Real Turn](#one-turn)
- [Stuck, Thrash, and Duplicate Detection](#stuck-detection)
- [The Premature-Finish Guard](#premature-finish)
- [Dynamic Step Budget Scaling](#step-budget)
- [Failure Escalation Strategy](#failure-escalation)
- [Context Compaction & Windowing](#summarize-detail)
- [State Checkpointing & Crash Recovery](#checkpointing)
- [Finding Verification & Exploit Oracles](#finding-verification)
- [Content-Layer Threat Quarantining](#content-defense)

---

## Why a Graph and Not a Loop <a id="why-a-graph"></a>

Control flow in `Execute()` (`graph.go`) is governed by an immutable transition table:

```go
var graphEdges = map[string]map[string]string{
    nodeStep: {
        edgeContextFull: nodeSummarize,
        edgeBudgetOut:   nodeFinish,
        edgeRunCommand:  nodeTool,
        edgeFinish:      nodeFinish,
        edgeAskOperator: nodeAsk,
        edgeUpdatePlan:  nodeStep,
        edgeSendFull:    nodeStep,
        edgeStopTask:    nodeFinish,
        edgeHandoff:     nodeHandoff,
        edgeRemember:    nodeRemember,
        edgeLLMError:    nodeRetry,
    },
    // ... complete transition map per node
}
```

Every node implements `func(ctx, *GraphState) (nextEdge string, err error)` and returns an edge name. The routing table maps `(currentNode, edge) -> nextNode`, decoupling node logic from global graph topology.

---

## The 11 State Machine Nodes <a id="the-nodes"></a>

| Node | File | Purpose |
|---|---|---|
| **`step`** | `node_step.go` | Core reasoning node — renders task state (plan, observations, findings, skills, memory) into a fresh prompt and queries the LLM. |
| **`tool`** | `node_tool.go` | Validates command arguments, checks for duplicates, and executes via `executor.RunScopedCommand`. |
| **`consult`** | `node_consult.go` | Intermediate check-in with the LLM when facing ambiguous states (long-running commands, repetition). |
| **`retry`** | `node_lifecycle.go` | Exponential backoff and retry following LLM network or transport errors. |
| **`summarize`** | `node_lifecycle.go` | Compresses observation history into an authoritative running summary under token pressure. |
| **`approval`** | `node_lifecycle.go` | Blocks the task goroutine until operator grants approval (in `ask_permission` mode or for high-risk commands). |
| **`threat_hold`** | `node_threat.go` | Content-layer quarantine node — pauses execution when tool output trips prompt-injection patterns. |
| **`finish`** | `node_lifecycle.go` | Terminal node. Finalizes task state, generates server-recorded evidence digest, and returns summary. |
| **`ask`** | `node_lifecycle.go` | Terminal pause node. Halts execution and presents a question to the operator. |
| **`handoff`** | `node_handoff.go` | Delegates subtasks concurrently to specialist sub-agents (depth-1 bounded). |
| **`remember`** | `node_remember.go` | Saves a structured memory entry to `knowledge.Store` via the `[REMEMBER]` tag. |

---

## Complete Routing Table <a id="routing-table"></a>

```text
step        -> context_full   -> summarize
            -> budget_out     -> finish
            -> run_command    -> tool
            -> finish         -> finish
            -> ask_operator   -> ask
            -> update_plan    -> step
            -> send_full      -> step
            -> stop_task      -> finish
            -> handoff        -> handoff
            -> remember       -> remember
            -> llm_error      -> retry

tool        -> ok / is_error       -> step
            -> requires_approval   -> approval
            -> stuck / long_running -> consult
            -> threat_hold         -> threat_hold

consult     -> continue / change_goal / ok / is_error -> step
            -> abort                                   -> step
            -> stop_task                                -> finish
            -> give_up                                   -> ask
            -> llm_error                                  -> retry
            -> long_running                                -> consult (self-loop)

retry       -> give_up        -> ask
            -> retry_again    -> (dynamic: returns to caller node)

approval    -> approved / denied -> step
            -> give_up            -> ask

threat_hold -> approved / denied -> step
            -> give_up            -> ask

summarize   -> done -> step

handoff     -> ok -> step

remember    -> ok -> step
```

`finish` and `ask` contain no outgoing transitions in `graphEdges`, terminating graph execution cleanly.

---

## Execution Trace of a Real Turn <a id="one-turn"></a>

```text
step     -> LLM returns [TOOL] nmap -sV 10.0.0.5 -> edge run_command -> tool
tool     -> Risk evaluated (Medium)               -> edge requires_approval -> approval
approval -> Operator clicks "Approve"             -> Executes inline -> edge approved -> step
step     -> LLM returns [TOOL] nikto -h 10.0.0.5  -> edge run_command -> tool
tool     -> Command completes with output         -> edge ok -> step
step     -> LLM returns [FINISH] + [FINDING]      -> edge finish -> finish
```

---

## Stuck, Thrash, and Duplicate Detection <a id="stuck-detection"></a>

To prevent small local models from looping unproductive commands, `graph.go` monitors a sliding window (`stuckWindowSize = 6`) over normalized command hashes:
- **Exact-Repeat Detection (`isStuck`)**: Flags if the identical command hash appears >=3 times or in strict A->B->A->B alternation.
- **Tool-Thrash Detection (`toolThrash`, threshold 4)**: Catches semantic repetition across differing flags (e.g. `ps -ef`, `ps aux`, `ps -e` in rapid succession).
- Both signals route execution to `consult` for model reconsideration before escalating to the operator.

---

## The Premature-Finish Guard <a id="premature-finish"></a>

When a multi-part goal produces multiple plan steps, models occasionally attempt to terminate after completing only the first item. If `FINISH` is received while uncompleted steps remain in the plan and step budget exists, the graph bounces the turn with an `[INCOMPLETE_GOALS]` notice back to `step`. This guard fires at most once per task to prevent deadlock.

---

## Dynamic Step Budget Scaling <a id="step-budget"></a>

Rather than enforcing an arbitrary static limit (e.g., 15 steps), `ScaledStepBudget` dynamically scales available iterations based on goal complexity:

$$\text{Budget} = \min\left(80, \, 6 + 12 \times \text{num\_detected\_parts}\right)$$

A 5-part reconnaissance and assessment workflow receives 66 steps, allowing sufficient runway for deep enumeration without premature budget exhaustion.

---

## Failure Escalation Strategy <a id="failure-escalation"></a>

- Consecutive failures are tracked per task (`consecutiveFailures`).
- The first two failures emit standard `[TOOL_FAILED]` tags with error outputs.
- At the third consecutive failure (`failureLimit = 3`), the system emits a high-priority `[REPEATED_FAILURE]` directive compelling the model to change tactics, consult `--help`, or adjust its plan.

---

## Context Compaction & Windowing <a id="summarize-detail"></a>

When context length approaches token thresholds, `summarizeNode` engages:
1. **Head Preservation**: The first observation (containing initial target discovery and port maps) is strictly preserved.
2. **Tail Preservation**: The most recent observations are preserved intact.
3. **Middle Compression**: Intermediate verbose output is replaced with `[...older observations summarized...]` and compacted by an LLM call into a <=400-word factual digest.

---

## State Checkpointing & Crash Recovery <a id="checkpointing"></a>

`saveCheckpoint()` serializes node state, plan step progress, observation digests, and failure counters into SQLite on every transition. Upon service restart:
- In-flight tasks are restored to the nearest safe `step` boundary.
- Replay of dangerous uncommitted tool calls is avoided.
- Child sub-agent tasks are reconciled by the parent task runner.

---

## Finding Verification & Exploit Oracles <a id="finding-verification"></a>

Yummi enforces server-side verification before elevating a candidate vulnerability to `verified`:

```text
Candidate Finding Emitted by Model
              |
              v
   verifyFindingDetail()
              |
              +---> 1. OracleActivePoC: Validates SQLi error patterns, RCE output, SSRF metadata
              |
              +---> 2. OracleDOMExecution: Regex validation of XSS sinks (<script>, onerror=)
              |
              +---> 3. OracleOOBInteraction: Confirms DNS/HTTP interaction tokens (Interactsh)
              |
              +---> 4. OracleDifferential: Status code / content length / timing divergence
              |
              v
   Server-Side Verdict: [verified] (oracle confirmed) or [candidate] (unproven claim)
```

Findings cannot be asserted into `verified` status by the model narrative alone; deterministic evidence must exist in captured output streams.

---

## Content-Layer Threat Quarantining <a id="content-defense"></a>

- Output from every tool invocation is ANSI-stripped (`threatscan.StripANSI`).
- If tool output trips prompt-injection or exfiltration signatures (`ScopeContext`), execution diverts to `threat_hold`.
- The operator reviews the suspect output in the UI and decides whether to sanitize, redact, or discard it before it can influence model context.
