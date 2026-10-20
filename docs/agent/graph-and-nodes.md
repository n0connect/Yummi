[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Agent graph: nodes and edges

`orchestrator/internal/agent/graph.go` defines node names, handlers and `graphEdges`. `Execute` calls a handler and follows its returned edge. `finish` and `ask` are terminal nodes. Eino has its own native tool-calling loop; this routing table describes the graph backend.

## Nodes <a id="the-nodes"></a>

| Node | Handler file | Responsibility |
|---|---|---|
| `step` | `node_step.go` | Render current state, query the model and parse a control action. |
| `tool` | `node_tool.go` | Validate, apply command approval policy and dispatch shell/MCP calls. |
| `consult` | `node_consult.go` | Decide whether to continue, abort or change course after a long-running/repeated action. |
| `retry` | `node_lifecycle.go` | Retry model errors using the stored caller and retry policy. |
| `summarize` | `node_lifecycle.go` | Compact observations with the model under context pressure. |
| `approval` | `node_lifecycle.go` | Wait for a command verdict, then dispatch the frozen approved action. |
| `threat_hold` | `node_threat.go` | Quarantine suspect output until the operator accepts, redacts or discards it. |
| `finish` | `node_lifecycle.go` | Record terminal outcome and summary with an execution evidence digest. |
| `ask` | `node_lifecycle.go` | Pause the task with an operator question. |
| `handoff` | `node_handoff.go` | Run bounded specialist work and return its observations. |
| `remember` | `node_remember.go` | Save/update or forget a session note through shared `ApplyNote`. |
| `skill_view` | `node_skill.go` | Load one full playbook by exact name from the skill index. |

## Routing <a id="routing-table"></a>

```text
step -> context_full: summarize; budget_out/finish/stop_task: finish
        run_command: tool; ask_operator: ask; update_plan/send_full: step
        handoff: handoff; remember: remember; skill_view: skill_view; llm_error: retry

tool -> ok/is_error: step; requires_approval: approval
        stuck/long_running: consult; threat_hold: threat_hold

consult -> continue/abort/change_goal/ok/is_error: step
           stop_task: finish; give_up: ask; llm_error: retry; long_running: consult

retry -> give_up: ask; retry: saved caller (dynamic)
approval -> approved/denied/ok/is_error: step; give_up: ask; threat_hold: threat_hold
threat_hold -> approved/denied: step; give_up: ask
summarize -> done: step
handoff/remember/skill_view -> ok: step
```

`RUN_COMMAND` and `MCP_TOOL` both route through `tool`. Approval does not turn an MCP action into shell text: the pending arguments are copied/frozen and approved dispatch uses the same MCP executor and threat-output processing as automatic dispatch.

## Approval behavior

`ask_permission` gates every command unless the per-task override applies. That override skips only commands below high severity with no `ForceApprovalReason`. In `auto`, structural destructive floors still gate; a high risk label alone does not. Single-command conductor proposals use a separate risk-based approval policy. Gateway egress approval is another lifecycle, handled by the execution store and executor.

## Loop guards and budgets <a id="stuck-detection"></a>

The recent command window has six entries. `isStuck` detects three equal hashes or an A→B→A→B pattern; `toolThrash` detects four uses of the same first command token. Exact repeat and duplicate checks provide observations for reconsideration rather than evidence of success.

<a id="failure-escalation"></a>

Consecutive command failures escalate at `MaxConsecutiveFailures` (default three) to `[REPEATED_FAILURE]`; at twice that threshold `step` pauses for the operator. A successful command clears the failure streak.

`ScaledStepBudget(configured, numParts)` leaves the configured budget unchanged below two detected goal parts. Otherwise it returns `max(configured, min(80, 6 + 12*numParts))`. This is a reasoning/action budget, not a guarantee of that many successful commands. Eino counts actual model turns and has a separate bounded operator extension flow.

## Finishing and plan ownership <a id="premature-finish"></a>

Production graph states give a premature `FINISH` one reconsideration when plan items remain pending/running and at least two budget units remain. The model can ultimately finish with unfinished items; terminal status therefore does not prove full goal coverage. A terminal plan also has convergence handling in `node_step.go`.

Model plan updates go through `Store.ReplaceModelPlan`: future intentions can be pending or skipped, but execution statuses, command/results, errors and counters are server-owned. See [execution provenance](execution-provenance.md).

## Compaction and recovery <a id="summarize-detail"></a>

`summarizeInput` bounds the input sent to the summarizer to 8,000 bytes, prioritizing the first observation and recent tail. Even the first observation can be bounded when large. The model is asked for a factual summary of at most 400 words; this is an instruction, not deterministic preservation. On success the summary replaces the in-memory observation list; a failed summarization keeps observations and resets the token counter.

`saveCheckpoint` records the current graph position, bounded recent observations, summary, retry state, commands and failure count in the task store. On restart, the runner restores recoverable tasks at a safe `step` boundary; it does not blindly replay the interrupted tool call. This is recovery support, not an exactly-once transaction across an external tool and SQLite.

## Findings and threat output <a id="finding-verification"></a>

`verifyFindingDetail` checks a finding's cited evidence against the just-returned output, or a successful plan step's bounded stored result. Matching tolerates case, surrounding quotes and whitespace normalization. A match earns `verified`; missing evidence or a matching miss remains `candidate`; citing an unsuccessful step is `unsupported`.

`ClassifyOracleKind` then assigns a heuristic label such as `tool_evidence`, `active_poc`, `dom_execution`, `oob_interaction` or `differential`. It does not independently reproduce the exploit, observe browser execution or operate an OOB callback service. `EvaluateActiveProof` is a separate deterministic helper over supplied strings. These labels cannot justify a stronger claim than the captured evidence.

Tool output is ANSI-stripped before model observations. Successful output that trips threat patterns enters `threat_hold`. The operator may accept raw output, supply redacted text in the verdict reason, or deny it. Command and threat approvals carry distinct metadata.

Source: `graph.go`, `node_step.go`, `node_tool.go`, `node_lifecycle.go`, `node_threat.go`, `verdict.go`, `oracle.go`, `prompts.go` and `store.go` under `orchestrator/internal/agent`.
