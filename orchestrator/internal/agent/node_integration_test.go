package agent

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// realShellStore returns an execution.Store whose processor runs commands in a
// real local shell. This is NOT a mock — it actually executes the command and
// reports real exit state, exactly like the production docker-exec processor
// does, just against the local host instead of sandbox.
func realShellStore(t *testing.T) *execution.Store {
	t.Helper()
	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		out, err := osexec.CommandContext(ctx, "sh", "-c", rec.Command).CombinedOutput()
		return string(out), err
	})
	return es
}

// newToolState builds a GraphState wired to a real shell store and scripted LLM.
func newToolState(t *testing.T, store *Store, taskID string, ollama LLM, es *execution.Store, cfg Config) *GraphState {
	t.Helper()
	return &GraphState{
		taskID: taskID,
		store:  store,
		llm:    ollama,
		exec:   NewStoreExecer(es, 5*time.Second),
		gate:   NewGate(),
		cfg:    cfg,
		memCtx: MemoryContext{TokenBudget: 6000},
	}
}

func planStep(plan []PlanStep, id string) PlanStep {
	for _, p := range plan {
		if p.ID == id {
			return p
		}
	}
	return PlanStep{}
}

// TestGraph_FailedStep_MarksStepFailed: a command that exits non-zero must mark
// its plan step Failed (not Done).
func TestGraph_FailedStep_MarksStepFailed(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "run a failing command")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"nonexistent_cmd_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q", got.Status, got.LastError)
	}
	if st := planStep(got.Plan, "s1").Status; st != StepFailed {
		t.Fatalf("expected s1 StepFailed, got %s", st)
	}
}

// TestGraph_RepeatedFailures_EscalateThenHalt: distinct commands that keep
// failing must (a) escalate to a forceful REPEATED_FAILURE directive once the
// streak reaches the limit, and (b) halt to the operator at 2× the limit rather
// than burning the whole budget. This replaces fingerprint pre-blocking: every
// command runs and the model sees real failure output before being pushed to
// adapt.
func TestGraph_RepeatedFailures_EscalateThenHalt(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "keep failing")
	es := realShellStore(t)
	// Seven DISTINCT failing commands (distinct → not duplicate-blocked, not
	// stuck). With the default limit 3, the 3rd failure escalates and the 6th
	// triggers the operator halt.
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"nope1_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope2_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope3_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope4_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope5_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope6_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"nope7_xyz\",\"step_id\":\"s1\",\"risk_level\":\"low\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusPaused {
		t.Fatalf("expected paused (operator halt), got %s err=%q", got.Status, got.LastError)
	}
	if !strings.Contains(got.Question, "consecutive") {
		t.Fatalf("expected an operator-halt question, got %q", got.Question)
	}
	// The forceful directive must have been surfaced before the halt.
	sawEscalation := false
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "[REPEATED_FAILURE]") {
			sawEscalation = true
			break
		}
	}
	if !sawEscalation {
		t.Fatal("expected a [REPEATED_FAILURE] escalation before the halt")
	}
}

// echoMCPStore returns an execution.Store whose processor reports back the
// dispatched tool name and JSON args verbatim — real execution.Store/
// EnqueueMCP plumbing, not a mock of agent's own code, just a stand-in
// processor for the sandbox-mcp docker-exec call (same relationship
// realShellStore has to the real docker-exec shell processor above).
func echoMCPStore(t *testing.T) *execution.Store {
	t.Helper()
	es := execution.NewStore()
	es.SetProcessor(func(_ context.Context, rec execution.Record) (string, error) {
		if rec.MCPTool == "" {
			return "", osexec.ErrNotFound // distinguishes a misrouted shell dispatch from a real MCP one
		}
		return rec.MCPTool + ":" + rec.MCPArgs, nil
	})
	return es
}

// TestGraph_MCPTool_DispatchesThroughSameExecutionStorePipeline proves graph's
// new [MCP_TOOL] action reaches execution.Store.EnqueueMCP (Record.MCPTool
// set, not a plain shell Command) — the generic-tag architecture decided for
// graph↔sandbox-mcp parity, reusing StoreMCPExecer unchanged rather than
// building a second dispatch path.
func TestGraph_MCPTool_DispatchesThroughSameExecutionStorePipeline(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "open a terminal session")
	es := echoMCPStore(t)
	ollama := newFakeOllama(t,
		"[MCP_TOOL]\n{\"tool\":\"term_open\",\"args\":{\"id\":\"sess1\"},\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}")

	cfg := fastRetryCfg()
	cfg.MCPExec = NewStoreMCPExecer(es, 5*time.Second)
	s := newToolState(t, store, task.ID, ollama, es, cfg)
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q", got.Status, got.LastError)
	}
	sawDispatch := false
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "term_open") {
			sawDispatch = true
		}
	}
	if !sawDispatch {
		t.Fatalf("expected an observation referencing the dispatched term_open tool; got %v", s.memCtx.Observations)
	}
}

// TestGraph_LocalModel_RunCommandGuessOfKnownToolIsRewrittenToMCPTool is the
// regression test for the actual live failure: a local 20B model, even with
// a worked [MCP_TOOL] example in the prompt, kept emitting
// [RUN_COMMAND] {"command":"term_open --label check5"} instead — a strong
// CLI-shaped prior a single example didn't override. Proves the
// deterministic rewrite (node_tool.go's InferMCPToolFromShellGuess) catches
// this and dispatches through the REAL MCP pipeline anyway, turning a
// guaranteed "command not found" into a working call — gated on
// cfg.LocalModel so a cloud model's behavior is never touched.
func TestGraph_LocalModel_RunCommandGuessOfKnownToolIsRewrittenToMCPTool(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "open a terminal session, local model guessing CLI syntax")
	es := echoMCPStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"term_open --label check5\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}")

	cfg := fastRetryCfg()
	cfg.LocalModel = true
	cfg.MCPExec = NewStoreMCPExecer(es, 5*time.Second)
	cfg.MCPSchemaHints = map[string]string{"term_open": "label, cols, rows"}
	s := newToolState(t, store, task.ID, ollama, es, cfg)
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q (expected the rewrite to make this a real, successful MCP dispatch)", got.Status, got.LastError)
	}
	sawRealDispatch := false
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "term_open") && !strings.Contains(o, "not found") {
			sawRealDispatch = true
		}
	}
	if !sawRealDispatch {
		t.Fatalf("expected a successful term_open dispatch observation, got %v", s.memCtx.Observations)
	}
}

// TestGraph_LocalModel_UnknownVerbIsNeverRewritten proves the rewrite only
// ever fires for a REAL cataloged tool name — an ordinary shell command
// (even one that happens to look vaguely flag-shaped) must still run as a
// plain shell command, never silently redirected to MCP dispatch.
func TestGraph_LocalModel_UnknownVerbIsNeverRewritten(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "run an ordinary command")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo --label check5\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}")

	cfg := fastRetryCfg()
	cfg.LocalModel = true
	cfg.MCPSchemaHints = map[string]string{"term_open": "label, cols, rows"} // "echo" is NOT in this set
	s := newToolState(t, store, task.ID, ollama, es, cfg)
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q", got.Status, got.LastError)
	}
	if st := planStep(got.Plan, "s1").Status; st != StepDone {
		t.Fatalf("expected the ordinary shell command to run normally, got step status %s", st)
	}
}

// rejectingMCPStore mimics a real sandbox-mcp JSON-schema validation
// rejection: real processor/execution.Store plumbing, a canned failure in
// place of the actual docker-exec'd validator (same relationship every
// other fake processor in this file has to the real one).
func rejectingMCPStore(t *testing.T) *execution.Store {
	t.Helper()
	es := execution.NewStore()
	es.SetProcessor(func(_ context.Context, rec execution.Record) (string, error) {
		return "", fmt.Errorf(`validating "arguments": validating root: unexpected additional properties ["name"]`)
	})
	return es
}

// TestGraph_MCPTool_FailureIsEnrichedWithRealFieldNames is the regression
// test for a live-observed gap: jinx-20b guessed term_open's session field
// was "name"; the real field is "label". The raw schema-rejection error
// ("unexpected additional properties") never said so, and with no
// catalog-wide schema dump in the prompt (deliberately — see
// Config.MCPCatalog), the model had nothing to self-correct from. Proves
// MCPSchemaHints gets folded into the TOOL_FAILED observation on failure.
func TestGraph_MCPTool_FailureIsEnrichedWithRealFieldNames(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "open a terminal with a wrong field name")
	es := rejectingMCPStore(t)
	ollama := newFakeOllama(t,
		`[MCP_TOOL]`+"\n"+`{"tool":"term_open","args":{"name":"check1"},"step_id":"s1","risk_level":"low"}`,
		`[FINISH]`+"\n"+`{"summary":"done"}`)

	cfg := fastRetryCfg()
	cfg.MCPExec = NewStoreMCPExecer(es, 5*time.Second)
	cfg.MCPSchemaHints = map[string]string{"term_open": "label, cols, rows"}
	s := newToolState(t, store, task.ID, ollama, es, cfg)
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	sawHint := false
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "Valid fields for term_open: label, cols, rows") {
			sawHint = true
		}
	}
	if !sawHint {
		t.Fatalf("expected the schema-hint enrichment in an observation; got %v", s.memCtx.Observations)
	}
}

// TestGraph_MCPTool_WithoutMCPExecConfigured_BlocksNotCrashes: a deployment
// that never wired agent.Config.MCPExec (MCPCatalog should also be empty in
// that case, so the model is never told the capability exists — but if it
// still emits [MCP_TOOL] anyway) must get a recoverable COMMAND_BLOCKED, not
// a nil-pointer panic or a hard task failure.
func TestGraph_MCPTool_WithoutMCPExecConfigured_BlocksNotCrashes(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "try an mcp tool with nothing wired")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[MCP_TOOL]\n{\"tool\":\"term_open\",\"args\":{},\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg()) // fastRetryCfg has no MCPExec
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q (expected the task to recover and finish, not crash)", got.Status, got.LastError)
	}
}

// TestGraph_StopTask_TransitionsStopped: the model may stop the whole task.
func TestGraph_StopTask_TransitionsStopped(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "stoppable task")
	ollama := newFakeOllama(t, "[STOP_TASK]\n{\"reason\":\"operator wants to stop\"}")

	s := &GraphState{taskID: task.ID, store: store, llm: ollama, gate: NewGate(), cfg: fastRetryCfg(), memCtx: MemoryContext{TokenBudget: 6000}}
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusStopped {
		t.Fatalf("expected stopped, got %s (err %q)", got.Status, got.LastError)
	}
}

// TestGraph_SendFull_NoBudgetSpend: [SEND_FULL] re-emits prior output without
// consuming a step budget unit.
func TestGraph_SendFull_NoBudgetSpend(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "send full output")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo hello\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[SEND_FULL]\n{}",
		"[FINISH]\n{\"summary\":\"done\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q", got.Status, got.LastError)
	}
	// run_command (1) + finish (1); send_full must not have consumed budget.
	if got.StepsUsed != 2 {
		t.Fatalf("expected StepsUsed==2 (send_full free), got %d", got.StepsUsed)
	}
}

// TestGraph_ConsecutiveDuplicate_Blocked: the same command twice in a row must
// run only once; the second is DUPLICATE_BLOCKED. Also covers a whitespace-only
// variant, which must collapse to the same fingerprint.
func TestGraph_ConsecutiveDuplicate_Blocked(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "no duplicate execution")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo dup\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo  dup\",\"step_id\":\"s1\",\"risk_level\":\"low\"}", // whitespace-only variant must still collapse
		"[FINISH]\n{\"summary\":\"done\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}
	n := 0
	for _, r := range es.ListBySession("test-session", 100) {
		if strings.Contains(r.Command, "echo") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 echo execution (2nd blocked), got %d", n)
	}
}

func TestGraph_RepeatedBlockedDuplicateNeverEscapesResetWindow(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "keep duplicate blocked")
	es := realShellStore(t)
	responses := []string{}
	for range 4 {
		responses = append(responses, `[RUN_COMMAND]
{"command":"echo stable","step_id":"s1","risk_level":"low"}`)
	}
	responses = append(responses, `[FINISH]
{"summary":"done"}`)
	s := newToolState(t, store, task.ID, newFakeOllama(t, responses...), es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}
	runs := 0
	for _, record := range es.ListBySession("test-session", 100) {
		if strings.TrimSpace(record.Command) == "echo stable" {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("blocked duplicate escaped the abnormal-pattern reset: executions=%d", runs)
	}
}

// TestConsult_LongRunning_AbortCancelsCommand: a command past the soft timeout
// triggers a consult; [ABORT] cancels it and the task proceeds promptly.
func TestConsult_LongRunning_AbortCancelsCommand(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "abort a long command")
	es := realShellStore(t)
	cfg := fastRetryCfg()
	cfg.SoftTimeout = 20 * time.Millisecond
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"sleep 3\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[ABORT]\n{}",
		"[FINISH]\n{\"summary\":\"aborted and finished\"}")

	s := newToolState(t, store, task.ID, ollama, es, cfg)
	start := time.Now()
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}
	elapsed := time.Since(start)

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s err=%q", got.Status, got.LastError)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ABORT should have cancelled the 3s sleep; took %s", elapsed)
	}
	// Regression: an aborted long-running command used to leave its plan step
	// frozen at "pending" forever — consultNode's abort branch recorded a
	// TOOL_FAILED observation but never called markStepStatus, unlike every
	// other termination path (success, failure, gateway-block). Live-observed
	// on a real Web Red Team task: nikto ran long, got aborted, and the task
	// completed with real findings while every plan step still showed
	// "pending" — confusing for an operator looking at the plan view.
	if planStep(got.Plan, "s1").Status != StepFailed {
		t.Fatalf("expected the aborted step (s1) to be marked failed, got %s", planStep(got.Plan, "s1").Status)
	}
}

// TestConsult_AbnormalPattern_RecoversAutonomously: an A→B→A→B loop surfaces an
// [ABNORMAL_PATTERN] warning to the model (no operator pause); the model then
// picks a different command and finishes — autonomous recovery.
func TestConsult_AbnormalPattern_RecoversAutonomously(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "break out of a loop")
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo a\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo b\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo a\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo b\",\"step_id\":\"s1\",\"risk_level\":\"low\"}", // triggers ABNORMAL_PATTERN (not executed)
		"[RUN_COMMAND]\n{\"command\":\"echo c\",\"step_id\":\"s1\",\"risk_level\":\"low\"}", // recovery: different command
		"[FINISH]\n{\"summary\":\"recovered\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("expected autonomous recovery to completed, got %s (err %q)", got.Status, got.LastError)
	}
	// The recovery command must have actually run (proves no premature pause).
	sawRecovery := false
	for _, r := range es.ListBySession("test-session", 100) {
		if r.Command == "echo c" {
			sawRecovery = true
		}
	}
	if !sawRecovery {
		t.Fatal("recovery command 'echo c' never executed")
	}
}

// TestConsult_Unresolved_EscalatesToOperator: a long-running command whose
// consult reply is unparseable is aborted; if the model keeps emitting garbage,
// the parse-retry budget is exhausted and the agent pauses for the operator.
func TestConsult_Unresolved_EscalatesToOperator(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "unresolvable consult")
	es := realShellStore(t)
	cfg := fastRetryCfg()
	cfg.SoftTimeout = 20 * time.Millisecond
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"sleep 1\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"this reply has no control tag at all")

	s := newToolState(t, store, task.ID, ollama, es, cfg)
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusPaused {
		t.Fatalf("expected paused (escalated to operator), got %s", got.Status)
	}
}
