← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Agent Quality & Evaluation

Yummi treats an agent transcript as evidence to inspect, not as proof that the task was done well. The quality layer therefore grades persisted task state and captured execution records with model-independent checks. It does not ask the model to grade its own answer.

## Two complementary contracts

### Deterministic persona boundary fixtures

`persona/v0.1/golden_tests/*.yaml` contains small input → expected-intent fixtures. `conductor.EvaluateGoldenFixtures` runs the deterministic boundary policy before an LLM is involved:

- unrestricted requests such as “scan the internet” must be declined;
- execution-shaped requests without an explicit host, URL, or file target must ask for clarification;
- educational/explanatory questions remain available to the model instead of being forced into an execution decision.

An uncovered case is reported as `llm_required`, not silently counted as a pass. Run the contract with:

```bash
cd orchestrator && go test ./internal/conductor -run TestPersonaGoldenFixtures
```

These fixtures protect the high-confidence safety boundary. They are not a replacement for testing the model's complete response quality.

### Objective task quality

`agent.EvaluateTaskQuality` evaluates a persisted `Task` against a `QualitySpec`. The evaluator counts actual plan/execution state and derives its report server-side:

| Metric | What it measures |
|---|---|
| `successful_steps` | Plan steps that reached `done` |
| `failed_steps` / `observed_steps` | Explicit failure and total observed-step counts |
| `duplicate_commands` | Repeated normalized commands that were actually dispatched; blocked proposals are not executions |
| `unverified_findings` | Findings without a server-verified evidence record, including unmatched legacy free-text findings |
| `failures` | Threshold, required-command, digest, or forbidden-summary violations |

The contract can require terminal completion and a non-empty summary, set minimum successful/observed steps, cap failures and duplicates, require the `EVIDENCE DIGEST (server-recorded)` marker, require one of several command families, and reject known failure phrases. A finding is only `verified` when the cited step completed successfully and its evidence is a literal, case-insensitive substring of captured tool output; `candidate` and `unsupported` findings remain visible but fail a zero-unverified threshold.

The evaluator is deliberately conservative: a substring match proves traceability to captured output, not that a vulnerability was exploited. A model narrative is interpretation; the evidence digest, step status, command, output, and finding verdict are the auditable record.

## Live quality gate

`TestLiveEval_Quality` runs the production-shaped graph against a real Ollama model and a sandbox command path using UID `1000:1000`, the checked-in agent persona, and the checked-in skill registry. The current scenarios cover hostname, multi-part system inspection, memory, database presence, largest files, and sudo/group inspection. Each scenario declares its own minimum observed/successful steps, required command families, and forbidden summary markers.

The test is opt-in because it needs Ollama, the sandbox, and a long runtime:

```bash
make test-agent-eval LIVE_TEST_MODEL=qwen3:14b
```

Equivalent direct invocation:

```bash
cd orchestrator
YUMMI_EVAL=1 \
YUMMI_OLLAMA_URL=http://127.0.0.1:11434 \
YUMMI_OLLAMA_MODEL=qwen3:8b \
go test ./internal/agent/ -run TestLiveEval_Quality -v -timeout 45m
```

Useful controls:

- `YUMMI_EVAL_CASE=E5-largest` runs one scenario;
- `YUMMI_SANDBOX_CONTAINER` selects the sandbox container;
- `YUMMI_PERSONA_DIR` selects the persona/skills directory;
- `LIVE_TEST_MODEL` controls the Make target's model tag.

The test prints the plan, executed commands, bounded outputs, step states, summary, findings, and the quality report. A failure is actionable: it identifies the violated threshold instead of merely returning a transcript for subjective review.

## What this does not claim

The quality gate is an acceptance contract for agent behavior and traceability, not an exploit oracle, vulnerability scanner, or universal benchmark. Required command groups encode the minimum evidence-gathering route for each scenario; they do not claim that one command is the only valid solution. Live results also depend on the selected local model, Ollama latency, sandbox image, and tool availability, so changes to those inputs should be recorded alongside the evaluation result.

When adding a regression, prefer a deterministic golden fixture for a boundary rule, an offline `QualitySpec` test for evaluator behavior, or a live scenario only when the property genuinely requires a real model and sandbox.
