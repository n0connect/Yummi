package agent

import (
	"reflect"
	"testing"
)

// The planner supplies intent, not an execution receipt. This exercises the
// real parser on an adversarial model message, without a provider double.
func TestParsePlanCannotInjectExecutionHistory(t *testing.T) {
	steps, err := ParsePlan(`{"steps":[{"id":"s1","description":"Inspect service","status":"done","command":"nmap example.com","result":"scan completed","error":"invented failure","duration":123,"retry_count":9}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s := steps[0]
	if s.Status != StepPending || s.Command != "" || s.Result != "" || s.Error != "" || s.Duration != 0 || s.RetryCount != 0 {
		t.Fatalf("model injected execution metadata into a new plan: %+v", s)
	}
}

func TestModelPlanCannotRewriteOrDropRunnerOwnedProgress(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("provenance", "Any goal", 10)
	if err != nil {
		t.Fatal(err)
	}
	// These are store-owned state transitions, not a substituted executor.
	// The test exercises the trust boundary between runner writes and model
	// writes; it does not claim to test command execution.
	_ = store.Update(task.ID, func(task *Task) {
		task.Plan = []PlanStep{{ID: "attempt", Description: "First attempt", Status: StepPending},
			{ID: "retry", Description: "Corrected attempt", Status: StepPending}}
	})
	markStepStatus(store, task.ID, "attempt", "first-command", StepFailed, "failed")
	markStepStatus(store, task.ID, "retry", "corrected-command", StepDone, "recorded result")
	before, _ := store.Get(task.ID)
	if err := store.ReplaceModelPlan(task.ID, []PlanStep{
		{ID: "retry", Description: "An unrelated invented action", Status: StepFailed, Command: "invented-command", Result: "invented result"},
		{ID: "next", Description: "Optional future work", Status: StepSkipped, Command: "never-ran", Result: "made up"},
		{ID: "retry", Status: StepDone},
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Get(task.ID)
	if len(after.Plan) != 3 || !reflect.DeepEqual(after.Plan[0], before.Plan[1]) || !reflect.DeepEqual(after.Plan[2], before.Plan[0]) {
		t.Fatalf("model rewrote, duplicated or removed runner history: %+v", after.Plan)
	}
	if after.Plan[1].Status != StepSkipped || after.Plan[1].Command != "" || after.Plan[1].Result != "" {
		t.Fatalf("skip decision was turned into an execution claim: %+v", after.Plan[1])
	}
	// A failed earlier attempt never downgrades the later recorded success.
	if after.Plan[0].Status != StepDone {
		t.Fatal("successful retry was vetoed by an earlier failure")
	}
}
