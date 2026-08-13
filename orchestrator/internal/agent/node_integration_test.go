package agent

import (
	"context"
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
