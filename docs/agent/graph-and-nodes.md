← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# The Agent Graph — Nodes & Edges

The agent's task loop is implemented as an explicit graph state machine in `orchestrator/internal/agent/graph.go`, not a `while` loop with branches. This page explains every node, why the graph is shaped the way it is, and the handful of cross-cutting mechanisms (stuck detection, checkpointing, failure escalation) that make the whole thing resilient against small, occasionally-unreliable local models.

## Table of contents

- [Why a graph and not a loop](#why-a-graph)
- [The 11 nodes](#the-nodes)
- [The routing table](#routing-table)
- [Walking one real turn](#one-turn)
- [Stuck, thrash, and duplicate detection](#stuck-detection)
- [The premature-FINISH guard](#premature-finish)
- [Step budget: flat default vs. scaled to goal complexity](#step-budget)
- [Failure escalation](#failure-escalation)
- [Context compaction: how `summarize` actually works](#summarize-detail)
- [Checkpointing and crash recovery](#checkpointing)
- [The `finish` node's synthesis fallback](#finish-node)
- [Finding verification: verdicts, not assertions](#finding-verification)
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
| **`step`** | `node_step.go` | The core reasoning node — renders the full task state (plan, observations, findings, skills, memory) into a fresh prompt and asks the LLM what to do next. Every step re-renders from scratch rather than relying on conversation history, so a local model does not lose state to a truncated history window. |
| **`tool`** | `node_tool.go` | Runs a validated command via the executor chain (never directly) and calls `observeResult` to interpret what came back. |
| **`consult`** | `node_consult.go` | A lighter-weight "check in with the model" path for ambiguous situations (a long-running command, an abnormal repetition pattern) that don't yet warrant escalating all the way to the operator. |
| **`retry`** | `node_lifecycle.go` | Backs off and retries after an LLM call itself fails (network error, malformed response) — distinct from a *command* failing, which is handled entirely differently (see [Failure escalation](#failure-escalation)). |
| **`summarize`** | `node_lifecycle.go` | Compresses observation history into a running summary when the token budget is under pressure — see [Context compaction](#summarize-detail) below. |
| **`approval`** | `node_lifecycle.go` | In `ask_permission` mode, blocks on the operator gate for every command, then runs it on approval (folding exec + observe into the node). `auto` bypasses this command gate. |
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

Concretely, "scan this host, then check for common web vulnerabilities" in `ask_permission` mode might walk like this:

```
step   → model replies RUN_COMMAND (nmap -sV) → edge run_command  → tool
tool   → command needs mode-driven approval    → edge requires_approval → approval
approval → operator approves → nmap runs, observed inline → edge approved → step
step   → model replies RUN_COMMAND (nikto)      → edge run_command → tool
tool   → command needs mode-driven approval     → edge requires_approval → approval
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

## The premature-FINISH guard <a id="premature-finish"></a>

A multi-part goal that correctly decomposed into several plan steps can still fail at the finish line: a model — regardless of size — frequently answers the *first* part of a goal and emits `FINISH` anyway, and nothing downstream re-opens the loop on its own. `stepNode` catches this deterministically: if `FINISH` arrives while `pendingStepDescriptions(task.Plan)` is non-empty (steps still `pending`/`running`) and at least two steps of budget remain, it's bounced back as an `[INCOMPLETE_GOALS]` observation listing exactly which parts are unaddressed, routed to `edgeUpdatePlan` instead of `edgeFinish`. This fires **at most once** per task (`maxIncompleteFinishNudges = 1`) — a remaining part that's genuinely unreachable must not wedge the loop in a bounce cycle, so after one forced reconsideration the model's decision is trusted. `BuildStepPrompt` also restates the same open-parts list every turn (independent of whether a bounce has happened yet), which makes the guard fire less often in practice — most of its value is the standing reminder, not the correction.

## Step budget: flat default vs. scaled to goal complexity <a id="step-budget"></a>

`StepBudget` is not always the flat configured default (15). `createAndPlan` computes `ScaledStepBudget(configured, numParts)` (`orchestrator/internal/agent/prompts.go`, shared with the Eino backend) from however many distinct parts `SplitEnumeratedParts`/`AuditGoalParts` detected in the goal, and persists the result onto the task in the same `Transition` call that stores the plan — so `stepNode`'s very first budget check (`task.StepsUsed >= task.StepBudget`) already sees the real ceiling, not the stale flat one. A single-intent goal (0 or 1 detected parts) is left untouched at the configured value.

The formula (`baseOverhead=6, perPart=12`, capped at `maxScaledBudget=80`) exists because a flat budget is a poor proxy for real work: live-observed against a real target with real cataloged pentest tools, a goal with only **two** requested parts (gobuster + nikto) never even reached the second part, because one tool hitting real-world friction (a missing wordlist) alone was enough to burn the entire flat-15 budget. A comprehensive 5-part recon+vuln workflow (nmap → directory enumeration → fingerprinting → nikto → summary) against a real target scaled to 66 and completed using 59 of them — the old flat default would have failed this exact task at step 15, discarding nmap's already-completed results along with everything else. `BuildStepPrompt` also renders ratio-based `BUDGET LOW`/`BUDGET CRITICAL` guidance once the remaining fraction crosses a threshold, so the model has explicit signal to converge rather than just a bare, easy-to-under-weight step count — see [Tag Protocol § not every recurring signal is a tag](tag-protocol.md).

## Failure escalation <a id="failure-escalation"></a>

A single failed command produces a plain `TOOL_FAILED` tag with the reason and output — the model is expected to just adapt. `consecutiveFailures` tracks a running streak (reset by any success), and at `failureLimit()` (default 3, configurable via `MaxConsecutiveFailures`), the *same* failure escalates to a forceful `REPEATED_FAILURE` tag that explicitly instructs the model to stop repeating the approach and do one of: try a different tool/route, check `--help`/`-h`, update the plan, or finish/ask — rather than silently blocking the retry outright. This was a deliberate design choice over "just block the third identical failure": the model always sees the real failure output and is *pushed* to adapt, rather than being prevented from ever trying again, which live testing showed left weaker models stuck with no path forward.

## Context compaction: how `summarize` actually works <a id="summarize-detail"></a>

`summarizeNode` fires on the `context_full` edge out of `step` — the model's step budget is under real token pressure, not just "a lot has happened." A naive fix would be to just cut the oldest observations and keep the tail, but that specifically throws away the *first* observation, which is usually the one that established the target, its topology, and its open ports — exactly the grounding the model needs most for everything that follows. Instead, `summarizeInput` protects **both ends**: it reserves up to a third of its character budget for the first observation (truncating it if it's unusually large, so one huge banner can't crowd out everything else) and fills the rest with as much of the recent tail as fits, replacing only the *middle* with an explicit `[…older observations omitted to fit context…]` marker. When the whole history already fits the budget, nothing is trimmed at all — compaction only touches history once it's actually over budget.

The assembled text (previous running summary, if any, plus the newly-windowed observations) goes to the LLM with one specific instruction: consolidate into **≤400 words**, but explicitly preserve target IPs, active ports, discovered credentials, paths/URLs, and vulnerabilities — the categories of fact a pentest task cannot afford to quietly lose to summarization. On success, the new summary replaces the old one, the token counter resets to zero, and the raw observation list is cleared (the summary is now the record). If the summarization LLM call itself fails, the node doesn't fail the task — it logs a warning, resets the token counter anyway, and continues without compression, trading a possibly-oversized context for a task that keeps running rather than one that halts on a transient LLM hiccup.

## Checkpointing and crash recovery <a id="checkpointing"></a>

`saveCheckpoint()` persists the node, retry state, token usage, running summary, last observation, recent proposals, commands actually dispatched, bounded working-memory observations, and consecutive-failure count on every transition. Proposal history and execution history are distinct, so a command rejected by duplicate/stuck guards is not later counted as an executed duplicate. After an orchestrator restart, an active top-level task is moved to a safe `step` boundary and automatically resumed with an explicit recovery observation. The loop does **not** blindly re-enter an in-flight `tool`, approval, or hand-off node, because replaying an uncertain side effect is more dangerous than asking the model to verify state first. Active child tasks are stopped and reconciled by their recovered parent so parent and child cannot both duplicate the same work. Planning calls that never produced a plan cannot be resumed and become failed with a clear reason.

Task persistence is committed before the in-memory store publishes a transition. A SQLite failure therefore returns an error without advancing only the in-memory copy.

## The `finish` node's synthesis fallback <a id="finish-node"></a>

Weak models frequently never emit `FINISH` on their own — they run out of step budget, or the loop otherwise terminates without a clean summary. Once a plan is terminal, the model gets one final opportunity to emit structured `FINISH`; a malformed/unavailable response then converges deterministically instead of opening more work or pausing solely on formatting. `finishNode` can run one additional synthesis call over accumulated observations, but fallback synthesis is not allowed to mint findings because it has no evidence-bearing `FindingDetail`.

Every completed or failed report ends with a bounded `EVIDENCE DIGEST (server-recorded)` built from persisted step status, command, and output. Model prose is explicitly labeled `MODEL NARRATIVE (unverified; evidence digest is authoritative)`. This makes the trust boundary visible: the narrative is useful interpretation, while the digest and per-finding verdicts are the auditable source of truth.

A related, separately-discovered safeguard lives right next to this: `unverifiedToolWarning` cross-checks the model's *claimed* findings against which commands actually completed successfully. This exists because of a real observed failure mode during live testing — a model reported "nikto detected known vulnerabilities" for a nikto invocation that had been aborted **twice** with zero real output. The fix deliberately does not strip or silently rewrite the claim (only the operator can judge whether a specific finding is actually accurate) — it appends a visible `⚠ UNVERIFIED` warning naming the suspect tool, making the risk visible rather than either blindly trusting or silently censoring the model's own report.

## Finding verification: verdicts, not assertions <a id="finding-verification"></a>

`unverifiedToolWarning` above is a blanket, whole-summary red flag. `orchestrator/internal/agent/verdict.go` adds a second, finer-grained mechanism: a **per-finding verdict** the model cannot assert into existence.

The model may attach `findings_detail` (instead of, or alongside, plain-text `findings`) to a `RUN_COMMAND` or `FINISH` reply — each entry an object with `title`, `evidence` (a verbatim quote from the tool output it's citing), and `step_id` (which plan step proves it). `verifyFindingDetail` then computes one of three verdicts **server-side**, never from a field the model sets directly:

| Verdict | Meaning |
|---|---|
| `candidate` | The honest default for anything not positively confirmed: no evidence/step cited, a cited step not in the plan, **or** a step that completed but whose output doesn't contain the claimed evidence verbatim. That last case is deliberately an *abstain*, not a red flag — a substring-match miss only proves this simple checker couldn't confirm the exact wording (the model may have paraphrased, or synthesized a fact from more than one line of output); it does not prove the claim is false. |
| `verified` | The claimed evidence is a real, literal (case-insensitive) substring of the cited step's actual captured output, and that step completed successfully. Carries `oracle_kind: "tool_evidence"` — naming the mechanism that earned it, the same way a `Receipt` can't claim `verified` without naming an oracle. |
| `unsupported` | Reserved for the one case that's a genuine, structural problem with the citation itself: the cited step failed or never completed. If the tool never produced output, nothing it "found" has a real source — stronger than "couldn't confirm the wording." |

Verification prefers the **live bridge result** of a just-run command (`observeResult` in `graph.go` has `result.Content` in hand, capped at 1 MiB per stream, before prompt compression) over the persisted `PlanStep.Result`, which is capped at 500 characters — evidence quoted later in a long result would otherwise be clipped away and wrongly read as unconfirmed. A `FINISH`-time finding isn't tied to a just-run step, so it can only be checked against the cited step's stored result — an honest limitation of a substring-match oracle rather than active exploit reproduction.

An oracle miss is not treated as proof that a claim is false. This verifier can establish traceability to captured output or identify a structurally invalid citation; it cannot generally disprove a security finding.

This is deliberately named `tool_evidence`, not `exploit_verified` or similar: it proves the claim is traceable to real captured output, not that a vulnerability was actually exploited. Yummi does not (yet) re-run an exploit to prove code execution the way an active oracle would; `OracleKind` exists specifically so a stronger oracle can be added later without disturbing anything that already consumes a `FindingRecord`.

Records persist on `Task.FindingRecords` (`finding_records` over the wire) alongside the existing flat `Findings`/`findings` — additive, so existing clients keep working. TaskTracker renders verdict-bearing records and preserves any unmatched plain findings as candidate claims; a partial record set therefore never hides legacy findings.

## Content-layer defense inside the graph <a id="content-defense"></a>

Two places inside the graph apply Yummi's [content-layer security](../security-model.md#layer-content) directly, and both matter enough to call out here rather than only in the security page:

- **Every tool result** is ANSI-stripped (`threatscan.StripANSI`) *before* anything else happens to it — the interpretation, the observation logging, and the threat scan itself all see clean text, closing off a class of terminal-escape-sequence injection tricks. Only the raw bytes in the execution store's own audit record keep the original formatting.
- **A threat-scan hit on tool output routes to `threat_hold`, not straight into the prompt.** The output is quarantined (`s.pendingThreatResult`) and the graph pauses on a separate operator gate — the operator can approve the raw output, deny/discard it, or supply a redacted replacement, and only that decision determines what (if anything) the model ultimately sees.
