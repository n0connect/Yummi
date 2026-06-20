package agent

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver used across n1 (session/execution)
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStoreCreateGetUpdate(t *testing.T) {
	s, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	task, err := s.Create("sess-1", "enumerate the n0 host", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.ID == "" || task.Status != StatusPlanning || task.StepBudget != 15 {
		t.Fatalf("unexpected task: %+v", task)
	}

	task.Plan = []PlanStep{{ID: "s1", Description: "whoami", Status: StepPending}}
	if err := s.Transition(task.ID, StatusPlanned, func(tk *Task) { tk.Plan = task.Plan }); err != nil {
		t.Fatalf("transition: %v", err)
	}

	got, ok := s.Get(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != StatusPlanned || len(got.Plan) != 1 || got.Plan[0].ID != "s1" {
		t.Fatalf("unexpected stored task: %+v", got)
	}
}

func TestStoreRejectsInvalidTransition(t *testing.T) {
	s, _ := NewStore(openTestDB(t))
	task, _ := s.Create("sess-1", "goal", 15)
	// planning → completed is illegal
	if err := s.Transition(task.ID, StatusCompleted, nil); err == nil {
		t.Fatal("expected invalid transition error")
	}
}

func TestStorePersistsAcrossReload(t *testing.T) {
	db := openTestDB(t)
	s1, _ := NewStore(db)
	task, _ := s1.Create("sess-1", "goal", 15)
	_ = s1.Transition(task.ID, StatusPlanned, nil)
	_ = s1.Transition(task.ID, StatusRunning, nil)

	// Second store on the same DB simulates a restart: active task → interrupted.
	s2, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := s2.Get(task.ID)
	if !ok {
		t.Fatal("task lost after reload")
	}
	if got.Status != StatusInterrupted {
		t.Fatalf("status after restart = %s, want interrupted", got.Status)
	}
}

func TestStorePublishesEvents(t *testing.T) {
	s, _ := NewStore(openTestDB(t))
	_, ch, err := s.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	task, _ := s.Create("sess-1", "goal", 15)
	evt := <-ch
	if evt.Type != "task.planning" || evt.Task.ID != task.ID {
		t.Fatalf("unexpected event: %+v", evt)
	}
}
