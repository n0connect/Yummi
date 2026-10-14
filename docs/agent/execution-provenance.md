# Execution-history blacklist

The model may propose actions and describe conclusions; it does not own
execution receipts. This control does not assess whether a user's goal was
achieved and does not require tools for an explanation-only task.

Blocked structural writes:

- Initial model plans cannot populate command/result/error/duration/retry
  metadata or start in an executed state.
- Graph and Eino model plan updates share `Store.ReplaceModelPlan`. New steps
  are pending or explicitly skipped, with no execution metadata. Existing
  runner-owned progress is preserved, including its original description;
  omissions and duplicate IDs cannot remove or duplicate recorded progress.
- Eino completion no longer converts pending steps to done or copies a model
  summary into execution results. Model completion remains distinct from
  recorded execution progress.

A failed attempt does not invalidate a subsequent recorded successful attempt.
No new failure/completion gate or task-specific heuristic is introduced.
Server-owned history here is the stored plan progress; this does not retroactively
certify historical data written before the boundary existed.

Free-form summaries and findings remain model claims. This blacklist does not
infer their meaning, guarantee their truth, or detect every hallucinated prose
claim. Actual execution cards/audit records remain separate from those claims.
No second LLM, host execution, network client or provider is added.
