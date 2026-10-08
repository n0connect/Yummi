<- [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# The Tag Protocol

Every message crossing the agent<->LLM boundary carries a tag from a small, closed vocabulary — `orchestrator/internal/agent/protocol.go`'s `Registry` is the single source of truth for all of it, and both the model's own system prompt and the agent's parser are generated from that one list. This page explains why this exists, what the vocabulary actually is, and how the parser stays robust when a model doesn't format its reply perfectly — which, for a small local model, is the normal case, not the exception.

**This robustness is a floor, not a guarantee of autonomous competence.** Live-tested (2026-10-07) against a real multi-stage CVE lab: even with this protocol, a 20B-class local model reliably lost track of the actual target across turns and needed server-side correction (`InferMCPToolFromShellGuess`, `node_tool.go`) just to use its own tool-calling tag correctly — the protocol kept its OUTPUT parseable, it did not make its REASONING reliable. For sustained autonomous multi-step work, the floor is a genuinely large model (120B+ local — e.g. `expert`/gpt-oss:120b, not a 70B-class or smaller tag — or a cloud override) — see `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry` doc comment for the full writeup and concrete model recommendations.

## Table of contents

- [Why a closed vocabulary at all](#why-closed-vocabulary)
- [Agent -> LLM: how observations are framed](#agent-to-llm)
- [LLM -> Agent: the control tags](#llm-to-agent)
- [Parsing a reply that isn't perfectly formatted](#robust-parsing)
- [Extending the protocol](#extending)

---

## Why a closed vocabulary at all <a id="why-closed-vocabulary"></a>

A tempting simpler design is to just hand the model raw command output and free-form instructions, and let it figure out from context what kind of message it's looking at. That works fine for a frontier model with a huge context window and strong instruction-following. The protocol's closed vocabulary exists because weaker local models — this design was originally built and tested around `qwen3:8b`-class models, though live testing has since shown even that is too weak for reliable autonomy — benefit enormously from **the same shape of message always meaning the same thing**, compared to free-form prose. `[TOOL_FAILED]` always means "a command ran but failed, don't repeat it verbatim." `[REPEATED_FAILURE]` always means "several failures in a row, you must genuinely change approach." The model learns this vocabulary once (from `ProtocolReference()`, rendered once into its system prompt) and then every single turn afterward reuses that exact same shared understanding, rather than the model having to re-infer "is this failure output the kind I should retry or the kind I should give up on" from prose that might phrase it slightly differently each time.

## Agent -> LLM: how observations are framed <a id="agent-to-llm"></a>

Every message the agent sends *to* the model is wrapped as `[TAG]\n<content>` by `Wrap()`. The full agent->LLM vocabulary, verbatim from the registry:

| Tag | Meaning |
|---|---|
| `TOOL_RESPONSE` | Command succeeded; output follows |
| `TOOL_RESPONSE_TRUNCATED` | Output too long; head+tail shown — reply `[SEND_FULL]` for the complete output |
| `TOOL_RESPONSE_FULL` | The complete output requested via `[SEND_FULL]` |
| `TOOL_FAILED` | Command ran but failed; reason included — adapt, don't repeat |
| `COMMAND_BLOCKED` | Malformed, or the gateway rejected the target as out of network scope |
| `DUPLICATE_BLOCKED` | This exact command was just run — choose a different one |
| `REPEATED_FAILURE` | Several commands failed in a row — genuinely change approach (see [Graph & Nodes § Failure escalation](graph-and-nodes.md#failure-escalation)) |
| `STUCK` | A repeated pattern was detected |
| `ABNORMAL_PATTERN` | Unusual repetition — deciding the next move together before escalating to the operator |
| `LONG_RUNNING` | A running command exceeded its soft time limit and is still going — reply `[CONTINUE]` or `[ABORT]` |
| `OUTPUT_OVERFLOW` | Terminal output is abnormally large and pressuring context |
| `APPROVAL_DENIED` | The operator denied a gated command — propose an alternative or finish |
| `APPROVAL_TIMEOUT` | Approval timed out |
| `PLAN_UPDATED` | The plan update was accepted |
| `INCOMPLETE_GOALS` | The model tried to `FINISH` while plan steps are still pending — bounced back once with the specific unaddressed parts listed, so it reconsiders before genuinely giving up (see [Graph & Nodes § the premature-FINISH guard](graph-and-nodes.md#premature-finish)) |
| `UNKNOWN` | The agent couldn't classify this situation; raw content follows — **highest priority**, the model decides |
| `OPERATOR_ANSWER` | The operator's reply to an `[ASK_OPERATOR]` question |

`UNKNOWN` deserves a specific callout: `Wrap()` degrades to it automatically whenever an unregistered tag name is passed in. This is a deliberate safety net — **the agent can never emit a label the model hasn't been told about**, so the protocol stays genuinely closed even if a future code change introduces a new internal tag string without also registering it. That's a fail-*visible* default (the model is told explicitly "I don't have a better classification for this, you decide"), not a silent swallow.

**Not every recurring signal is a tag.** Budget pressure used to be a one-shot `BUDGET_WARNING` tag fired via `Wrap()` — it was removed because it was declared in the registry and never actually emitted by any node, so the model's own system prompt described a signal it would never receive. Budget-regime guidance (`BUDGET LOW`/`BUDGET CRITICAL`, ratio-based against `StepBudget`) is instead rendered directly into `BuildStepPrompt` every turn, the same way the plan's [ ]/[x] status and the goal-parts-still-open list already are — a persistent piece of always-current state belongs in the per-turn prompt render, not in a one-shot observation that would only be true for the single turn it fired on.

## LLM -> Agent: the control tags <a id="llm-to-agent"></a>

Every reply the model sends back is expected to begin with one control tag on its own line, followed by a JSON body:

| Tag | Payload | Meaning |
|---|---|---|
| `RUN_COMMAND` | `{"command","step_id","rationale","risk_level","findings"?,"findings_detail"?}` | Run a shell command |
| `UPDATE_PLAN` | `{"steps":[...],"reason"}` | Revise the plan |
| `FINISH` | `{"summary","findings"?,"findings_detail"?}` | Goal achieved — see the note below about grounding findings |
| `HANDOFF` | `{"role","subgoal"}` or `{"handoffs":[{"role","subgoal"},...]}` | Delegate to one specialist, or several concurrently |
| `REMEMBER` | `{"key","value"}` | Persist session-scoped structured memory until an operator explicitly forgets it |
| `ASK_OPERATOR` | `{"question"}` | Blocked; need a human |
| `SEND_FULL` | `{}` | Resend the last truncated output in full |
| `CONTINUE` | `{}` | Keep waiting / proceed (reply to a consult) |
| `ABORT` | `{}` | Cancel the running command but keep the task |
| `STOP_TASK` | `{"reason"?}` | Stop the whole task |

`FINISH`'s registry entry carries an unusually specific instruction baked directly into its `Summary` field (which gets rendered straight into the model's own system prompt): *"Before writing findings, re-scan every `TOOL_RESPONSE` above for concrete, specific facts (exact ports/versions/paths/cookies) rather than a generic summary — a specific fact like 'port 3306 open, MySQL 5.5.47' is a real finding; 'open ports were found' is not."* This is worth reading as a small case study in prompt design: rather than a separate validation pass rejecting vague findings after the fact, the instruction to be specific is placed exactly where the model is about to write them, in the same message that tells it what tag to use.

## Parsing a reply that isn't perfectly formatted <a id="robust-parsing"></a>

`ParseControl()` is layered specifically because small models frequently *don't* lead with a clean `[TAG]`, even when told to:

1. **Preferred path** — a leading `[TAG_NAME]` matching a known LLM->agent tag, followed by its JSON body.
2. **Tagless JSON fallback** — if no valid tag is found but the reply contains a JSON object, `inferControlTag()` looks at the *shape* of the decoded payload to guess the tag: a non-empty `command` field means `RUN_COMMAND`, a `role`+`subgoal` pair (or a non-empty `handoffs` list) means `HANDOFF`, `key`+`value` means `REMEMBER`, and so on down a fixed precedence order.
3. **Bare keyword fallback** — a bodiless reply that's just the literal word `STOP_TASK`, `SEND_FULL`, `CONTINUE`, or `ABORT` with no brackets and no JSON at all still parses correctly.

Only if none of these three succeed does parsing return an error — which the caller's retry path handles, not a hard failure of the task. This tolerance is a direct, pragmatic response to how small models actually behave in practice, not a hypothetical robustness feature: a model that's 95% reliable at the exact format still fails the format 1-in-20 times, and without this fallback chain, that 5% would translate directly into wasted retries or stalled tasks rather than the agent just correctly inferring what was obviously meant.

An **unrecognized control tag** (a real `[SOMETHING_MADE_UP]` the model invented) is treated as an error — distinct from `[UNKNOWN]`, which is agent->LLM only and never something the model is expected to send. The model doesn't get a permissive "anything goes" channel back to the agent; it gets exactly the fixed vocabulary above, with graceful shape-inference as a safety net for formatting slips, not an open door for inventing new commands.

## Extending the protocol <a id="extending"></a>

The registry's own doc comment is explicit about this: **developers add or remove protocol tags by editing `Registry` only.** `Wrap()` and `ProtocolReference()` (the function that renders the whole vocabulary into the model's system prompt) both derive from that one list — there is no second place that needs to independently stay in sync. Adding a tag anywhere else in the codebase without registering it here means `Wrap()` will silently downgrade it to `[UNKNOWN]` at runtime, which is a deliberately loud, easy-to-notice failure mode (the model visibly gets told "I don't know what this is") rather than a silent one.
