← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# The Agent Graph — Nodes & Edges

The agent's task loop is implemented as an explicit graph state machine in `orchestrator/internal/agent/graph.go`, not a `while` loop with branches. This page explains every node, why the graph is shaped the way it is, and the handful of cross-cutting mechanisms (stuck detection, checkpointing, failure escalation) that make the whole thing resilient against small, occasionally-unreliable local models.

## Table of contents

- [Why a graph and not a loop](#why-a-graph)
- [The 11 nodes](#the-nodes)
- [The routing table](#routing-table)
- [Walking one real turn](#one-turn)
- [Stuck, thrash, and duplicate detection](#stuck-detection)
- [Failure escalation](#failure-escalation)
- [Context compaction: how `summarize` actually works](#summarize-detail)
- [Checkpointing and crash recovery](#checkpointing)
- [The `finish` node's synthesis fallback](#finish-node)
- [Content-layer defense inside the graph](#content-defense)

---

## Why a graph and not a loop <a id="why-a-graph"></a>

`Execute()` (`graph.go`) is genuinely small — under 40 lines — because almost all the complexity lives in the **data**, not the control flow:

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
    // ... one entry per node
}
```

Every node function has the same signature — `func(ctx, *GraphState) (nextEdge string, err error)` — and returns an **edge name**, not a next node. The routing table then maps `(currentNode, edge) → nextNode`. This indirection is the entire point: a node doesn't need to know or care what happens after it — `stepNode` returning `edgeRunCommand` doesn't hardcode "go to `toolNode`," it just names *what happened* ("the model wants to run a command"), and the table decides what that means structurally. You can read the whole possible-transitions map of the entire agent in one screen (reproduced in full below), independent of any node's internal logic — that's not true of a loop with branches spread across a 500-line function.

## The 11 nodes <a id="the-nodes"></a>

| Node | File | What it does |
|---|---|---|
| **`step`** | `node_step.go` | The core reasoning node — renders the full task state (plan, observations, findings, skills, memory) into a fresh prompt and asks the LLM what to do next. Every step re-renders from scratch rather than relying on conversation history, specifically so a small model like `qwen3:8b` never loses context to a truncated history window. |
| **`tool`** | `node_tool.go` | Runs a validated command via the executor chain (never directly) and calls `observeResult` to interpret what came back. |
| **`consult`** | `node_consult.go` | A lighter-weight "check in with the model" path for ambiguous situations (a long-running command, an abnormal repetition pattern) that don't yet warrant escalating all the way to the operator. |
| **`retry`** | `node_lifecycle.go` | Backs off and retries after an LLM call itself fails (network error, malformed response) — distinct from a *command* failing, which is handled entirely differently (see [Failure escalation](#failure-escalation)). |
| **`summarize`** | `node_lifecycle.go` | Compresses observation history into a running summary when the token budget is under pressure — see [Context compaction](#summarize-detail) below. |
| **`approval`** | `node_lifecycle.go` | Blocks on the operator gate for a medium+ risk command, then runs it itself on approval (folding the exec + observe steps in, rather than bouncing back to `tool`). |
| **`threat_hold`** | `node_threat.go` | A second, content-layer approval gate — pauses when a command's *output* (not the command itself) trips a threatscan pattern. See [Content-layer defense](#content-defense). |
| **`finish`** | `node_lifecycle.go` | Terminal. Ends the task as completed, failed, or stopped — with a synthesis fallback described [below](#finish-node). |
| **`ask`** | `node_lifecycle.go` | Terminal. Pauses the task with an operator-facing question; the task stays alive (it can be resumed) rather than ending. |
| **`handoff`** | `node_handoff.go` | Delegates one or more focused subtasks to specialist sub-agents **concurrently**, merges their results, and returns to `step`. See [Skills & Sub-agents](skills-and-subagents.md). |
| **`remember`** | `node_remember.go` | Persists a structured, never-forgotten memory element to the shared Knowledge Store. Knowledge-only — no execution path. See [Memory & Knowledge](memory-and-knowledge.md). |

## The routing table <a id="routing-table"></a>

Reproduced here in full because it *is* the agent's control flow — every possible transition, in one place:

```
step        → context_full   → summarize
            → budget_out     → finish
            → run_command    → tool
            → finish         → finish
            → ask_operator   → ask
            → update_plan    → step
            → send_full      → step
            → stop_task      → finish
            → handoff        → handoff
            → remember       → remember
            → llm_error      → retry

tool        → ok / is_error       → step
            → requires_approval   → approval
            → stuck / long_running → consult
            → threat_hold         → threat_hold

consult     → continue / change_goal / ok / is_error → step
            → abort                                   → step
            → stop_task                                → finish
            → give_up                                   → ask
            → llm_error                                  → retry
            → long_running                                → consult   (self-loop)

retry       → give_up        → ask
            → retry_again    → (dynamic — returns to whichever node called it)

approval    → approved / denied → step
            → give_up            → ask

threat_hold → approved / denied → step
            → give_up            → ask

summarize   → done → step

handoff     → ok → step
remember    → ok → step
```

`finish` and `ask` have no outgoing entries in the table at all — `Execute()` detects this (`graphEdges[current]` has no key) and treats it as terminal, stopping the walk regardless of what edge the node returned. This is a clean way to express "these are the only two ways a task run can end" without a separate `isTerminal()` check scattered through the code.

**`retry`'s dynamic routing is the one deliberate exception to "the table is everything."** Retrying after an LLM error needs to go back to *whichever node* originally tried to call the LLM (`step`, `consult`, or wherever), not to one fixed node — so `Execute()` special-cases `edgeRetryAgain` to read `s.retryCtx.Caller` instead of consulting the table. This is the one place the graph's uniformity gives way to something more dynamic, and it's called out explicitly in the code as the exception, not left for a reader to discover by surprise.

## Walking one real turn <a id="one-turn"></a>

Concretely, "scan this host, then check for common web vulnerabilities" might walk like this:

```
step   → model replies RUN_COMMAND (nmap -sV) → edge run_command  → tool
tool   → command succeeds, no approval needed  → edge ok          → step
step   → model replies RUN_COMMAND (nikto)      → edge run_command → tool
tool   → nikto's classified risk is "medium"    → edge requires_approval → approval
approval → operator approves → command runs, observed inline → edge approved → step
step   → model replies FINISH with a summary + findings → edge finish → finish
```

Nothing about this sequence is hardcoded anywhere as "nmap then nikto then finish" — every arrow is the model's own decision, re-derived from the freshly-rendered task state at each `step`. The graph only constrains *what kinds of things can happen next* from a given node, never *what specifically* happens.

## Stuck, thrash, and duplicate detection <a id="stuck-detection"></a>

Small local models occasionally get stuck — re-running the same command, or cycling between two commands (A→B→A→B) without making progress. `graph.go` catches this with a sliding window (`stuckWindowSize = 6`) over recent command hashes:

- **Exact-repeat detection** (`isStuck`): the same command hash appearing ≥3 times in the window, or a clean A→B→A→B alternation, both flag as stuck.
- **Tool-thrash detection** (`toolThrash`, threshold 4): catches a *different* failure mode exact-hash matching misses entirely — `ps -ef`, `ps -ejH`, `ps aux`, and `ps -e` are four distinct strings (different hashes) but the same tool producing the same class of information over and over. The threshold is deliberately set *above* 3 specifically so a legitimate escalation on one tool (`nmap -sV` → `nmap -p-` → `nmap -sC`) isn't flagged as thrashing — the check exists to catch genuine unproductive repetition, not normal iterative tool use.
- **Whitespace-normalized hashing** (`commandHash`): formatting jitter (a model re-emitting the same logical command with different spacing) still collapses to the same hash, so trivial cosmetic variation doesn't defeat exact-repeat detection.

Both signals route to `consult` — a lighter-weight "let's talk about this before I escalate you to a human" step, not an immediate hard stop.

## Failure escalation <a id="failure-escalation"></a>

A single failed command produces a plain `TOOL_FAILED` tag with the reason and output — the model is expected to just adapt. `consecutiveFailures` tracks a running streak (reset by any success), and at `failureLimit()` (default 3, configurable via `MaxConsecutiveFailures`), the *same* failure escalates to a forceful `REPEATED_FAILURE` tag that explicitly instructs the model to stop repeating the approach and do one of: try a different tool/route, check `--help`/`-h`, update the plan, or finish/ask — rather than silently blocking the retry outright. This was a deliberate design choice over "just block the third identical failure": the model always sees the real failure output and is *pushed* to adapt, rather than being prevented from ever trying again, which live testing showed left weaker models stuck with no path forward.

## Context compaction: how `summarize` actually works <a id="summarize-detail"></a>

`summarizeNode` fires on the `context_full` edge out of `step` — the model's step budget is under real token pressure, not just "a lot has happened." A naive fix would be to just cut the oldest observations and keep the tail, but that specifically throws away the *first* observation, which is usually the one that established the target, its topology, and its open ports — exactly the grounding the model needs most for everything that follows. Instead, `summarizeInput` protects **both ends**: it reserves up to a third of its character budget for the first observation (truncating it if it's unusually large, so one huge banner can't crowd out everything else) and fills the rest with as much of the recent tail as fits, replacing only the *middle* with an explicit `[…older observations omitted to fit context…]` marker. When the whole history already fits the budget, nothing is trimmed at all — compaction only touches history once it's actually over budget.

The assembled text (previous running summary, if any, plus the newly-windowed observations) goes to the LLM with one specific instruction: consolidate into **≤400 words**, but explicitly preserve target IPs, active ports, discovered credentials, paths/URLs, and vulnerabilities — the categories of fact a pentest task cannot afford to quietly lose to summarization. On success, the new summary replaces the old one, the token counter resets to zero, and the raw observation list is cleared (the summary is now the record). If the summarization LLM call itself fails, the node doesn't fail the task — it logs a warning, resets the token counter anyway, and continues without compression, trading a possibly-oversized context for a task that keeps running rather than one that halts on a transient LLM hiccup.

## Checkpointing and crash recovery <a id="checkpointing"></a>

`saveCheckpoint()` persists the current node name, retry context, token usage, and running summary to the task store on **every** transition — not just at milestones. This is what makes a task survive an orchestrator restart mid-run: on resume, the graph doesn't replay from the beginning, it re-enters `Execute()` at exactly the node it was last checkpointed at, with its accumulated state intact.

## The `finish` node's synthesis fallback <a id="finish-node"></a>

Weak models frequently never emit `FINISH` on their own — they run out of step budget, or the loop otherwise terminates without a clean summary. `finishNode` checks for this (`strings.TrimSpace(summary) == ""`) and, when it fires, runs **one additional LLM call** (`synthesizeSummary`) purely over the accumulated observations to produce a real report and concrete findings — with an explicit instruction to report only what the observations actually show, and say plainly when something was *not* found rather than claiming a detection that didn't happen. This guarantees the operator always gets a deliverable, even from a model that structurally can't reliably signal "I'm done" on its own — a small, cheap accommodation for weaker tiers that costs nothing when the strongest tier already finishes cleanly on its own.

A related, separately-discovered safeguard lives right next to this: `unverifiedToolWarning` cross-checks the model's *claimed* findings against which commands actually completed successfully. This exists because of a real observed failure mode during live testing — a model reported "nikto detected known vulnerabilities" for a nikto invocation that had been aborted **twice** with zero real output. The fix deliberately does not strip or silently rewrite the claim (only the operator can judge whether a specific finding is actually accurate) — it appends a visible `⚠ UNVERIFIED` warning naming the suspect tool, making the risk visible rather than either blindly trusting or silently censoring the model's own report.

## Content-layer defense inside the graph <a id="content-defense"></a>

Two places inside the graph apply Yummi's [content-layer security](../security-model.md#layer-content) directly, and both matter enough to call out here rather than only in the security page:

- **Every tool result** is ANSI-stripped (`threatscan.StripANSI`) *before* anything else happens to it — the interpretation, the observation logging, and the threat scan itself all see clean text, closing off a class of terminal-escape-sequence injection tricks. Only the raw bytes in the execution store's own audit record keep the original formatting.
- **A threat-scan hit on tool output routes to `threat_hold`, not straight into the prompt.** The output is quarantined (`s.pendingThreatResult`) and the graph pauses on the operator gate exactly like a risk-based command approval — the operator can approve the raw output, deny/discard it, or supply a redacted replacement, and only that decision determines what (if anything) the model ultimately sees.
