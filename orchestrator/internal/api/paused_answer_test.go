package api

import (
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// An operator reply must reach a task that is waiting for one, and must not
// be mistaken for an answer when no question is open.
func TestFindPausedTaskAwaitingAnswer(t *testing.T) {
	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	task, err := store.CreateSubtask("sess-answer", "goal", 15, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}
	if _, ok := findPausedTaskAwaitingAnswer(store, "sess-answer"); ok {
		t.Fatal("a planned task must not be treated as awaiting an answer")
	}
	if err := store.Transition(task.ID, agent.StatusPlanned, nil); err != nil {
		t.Fatalf("Transition planned: %v", err)
	}
	if err := store.Transition(task.ID, agent.StatusRunning, nil); err != nil {
		t.Fatalf("Transition running: %v", err)
	}
	if err := store.Transition(task.ID, agent.StatusPaused, func(x *agent.Task) { x.Question = "Step budget exhausted (15 steps)." }); err != nil {
		t.Fatalf("Transition paused: %v", err)
	}
	got, ok := findPausedTaskAwaitingAnswer(store, "sess-answer")
	if !ok || got.ID != task.ID {
		t.Fatalf("paused task with a question should be found; got ok=%v id=%s", ok, got.ID)
	}
	if _, ok := findPausedTaskAwaitingAnswer(store, "other-session"); ok {
		t.Fatal("must not match tasks from another session")
	}
}
