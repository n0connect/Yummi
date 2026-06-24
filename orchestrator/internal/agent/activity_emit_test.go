package agent

import (
	"context"
	"testing"
	"time"
)

func TestGraphEmitsActivity(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"identify user"}]}`,
		`[RUN_COMMAND]
{"command":"whoami","step_id":"s1","risk_level":"low","findings":["user found"]}`,
		`[FINISH]
{"summary":"done","findings":["confirmed"]}`,
	)
	cfg := fastRetryCfg()
	r := NewRunner(store, ollama, nil, NewStoreExecer(es, 5*time.Second), cfg)
	task, err := r.CreateAndPlan(context.Background(), "s1", "recon")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("run: %v", err)
	}
	acts := store.ListActivity("s1", 100)
	var sawPlan, sawCmd, sawCmdDone, sawFinding, sawFinish bool
	for _, a := range acts {
		switch a.Kind {
		case ActPlan:
			sawPlan = true
		case ActCommand:
			sawCmd = true
		case ActCommandDone:
			sawCmdDone = true
		case ActFinding:
			sawFinding = true
		case ActFinish:
			sawFinish = true
		}
	}
	if !sawPlan || !sawCmd || !sawCmdDone || !sawFinding || !sawFinish {
		t.Fatalf("missing activity: plan=%v cmd=%v done=%v finding=%v finish=%v (all=%+v)",
			sawPlan, sawCmd, sawCmdDone, sawFinding, sawFinish, acts)
	}
}
