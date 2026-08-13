package agent

import (
	"database/sql"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver used across orchestrator (session/execution)
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// A bare ":memory:" DSN hands each NEW pooled connection its own private,
	// empty database (no shared cache) — database/sql opens connections
	// lazily and can hand a concurrent goroutine a second physical connection
	// whose schema migration never ran, so its writes silently fail with "no
	// such table". Any test that exercises concurrent Store access (e.g. a
	// parallel HANDOFF fan-out, or CreateSubtask from multiple goroutines)
	// can hit this nondeterministically. Capping the pool at one connection
	// makes every query go through the exact same in-memory database, the
	// general fix for the whole package instead of a per-test workaround.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestGetDoesNotAliasPlanBackingArray is a regression test found via live
// stress-testing: hammering GET /api/v1/tasks/{id} while an agent task was
// actively running (markStepStatus writing per-step Result/Status) produced
// intermittent invalid JSON in the HTTP response — a literal control
// character inside a string field. Root cause: Store.Get returns a Task by
// value, but Task.Plan is a slice, so the returned copy shares the SAME
// backing array as the store's internal copy. markStepStatus's mutator does
// an IN-PLACE index write (t.Plan[i].Result = d) under the store's lock, but
// any earlier Get() caller is holding an aliased slice header and can read
// that same backing array concurrently, completely outside the lock — a
// genuine, `-race`-visible data race, not just a theoretical one. This
// exercises the exact shape live traffic hit: one goroutine repeatedly Get()s
// and reads every field of every returned PlanStep while another repeatedly
// Update()s a plan step's Result/Status in place.
func TestGetDoesNotAliasPlanBackingArray(t *testing.T) {
	s, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, err := s.Create("sess-race", "goal", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "d", Status: StepPending}}
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := s.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: repeatedly mutate the plan step in place, exactly like
	// markStepStatus does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.Update(task.ID, func(tk *Task) {
				for j := range tk.Plan {
					if tk.Plan[j].ID == "s1" {
						tk.Plan[j].Result = "result-text-that-must-never-tear"
						tk.Plan[j].Status = StepDone
					}
				}
			})
		}
	}()

	// Reader: repeatedly Get() and read every byte of the held snapshot's
	// Plan — a data race here (flagged by -race) proves the aliasing. Signals
	// the writer to stop once it's done, so wg.Wait() below can't deadlock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 2000; i++ {
			got, ok := s.Get(task.ID)
			if !ok {
				continue
			}
			for _, step := range got.Plan {
				_ = len(step.Result)
				_ = len(step.Description)
			}
		}
	}()

	wg.Wait()
}

func TestStoreCreateGetUpdate(t *testing.T) {
	s, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	task, err := s.Create("sess-1", "enumerate the sandbox host", 15)
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
	_ = s1.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "work", Status: StepPending}}
	})
	_ = s1.Transition(task.ID, StatusRunning, nil)

	// Second store on the same DB simulates a restart: an active top-level task
	// becomes a safe, explicitly recoverable paused checkpoint.
	s2, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := s2.Get(task.ID)
	if !ok {
		t.Fatal("task lost after reload")
	}
	if got.Status != StatusPaused || !got.Checkpoint.ResumeAfterRestart || got.Checkpoint.CurrentNode != nodeStep {
		t.Fatalf("task was not recovered safely after restart: %+v", got)
	}
}

// TestFindingRecordsPersistAcrossReload guards the same class of bug as
// TestCreateSubtaskPersistsParentTaskIDAcrossReload below: a new Task field
// needs its own column plus explicit persist()/loadFromDB() wiring, or it
// silently survives in memory only and vanishes the moment the orchestrator
// restarts — which for a verdict/oracle_kind field would quietly turn every
// past Verified finding back into an unlabeled one after a restart.
func TestFindingRecordsPersistAcrossReload(t *testing.T) {
	db := openTestDB(t)
	s1, _ := NewStore(db)
	task, _ := s1.Create("sess-1", "goal", 15)
	_ = s1.Update(task.ID, func(t *Task) {
		t.FindingRecords = []FindingRecord{
			{Title: "MySQL exposed", Verdict: VerdictVerified, OracleKind: OracleToolEvidence, Evidence: "3306/tcp open", StepID: "s1"},
		}
	})

	s2, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := s2.Get(task.ID)
	if !ok {
		t.Fatal("task lost after reload")
	}
	if len(got.FindingRecords) != 1 || got.FindingRecords[0].Verdict != VerdictVerified {
		t.Fatalf("finding records did not survive reload, got %+v", got.FindingRecords)
	}
}

// Regression: core_identity-style "whole field silently dropped" bugs aren't
// limited to YAML — ParentTaskID needs an explicit column + persist/load wire-
// up like every other Task field, or a sub-task's parent link would survive
// in memory but vanish across a restart, breaking the operator-discoverability
// fix this field exists for.
func TestCreateSubtaskPersistsParentTaskIDAcrossReload(t *testing.T) {
	db := openTestDB(t)
	s1, _ := NewStore(db)
	parent, _ := s1.Create("sess-1", "parent goal", 15)
	sub, err := s1.CreateSubtask("sess-1", "sub goal", 15, parent.ID)
	if err != nil {
		t.Fatalf("create subtask: %v", err)
	}
	if sub.ParentTaskID != parent.ID {
		t.Fatalf("ParentTaskID = %q, want %q", sub.ParentTaskID, parent.ID)
	}

	s2, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := s2.Get(sub.ID)
	if !ok {
		t.Fatal("subtask lost after reload")
	}
	if got.ParentTaskID != parent.ID {
		t.Fatalf("ParentTaskID after reload = %q, want %q", got.ParentTaskID, parent.ID)
	}

	// A top-level task must NOT pick up a stray parent link.
	gotParent, _ := s2.Get(parent.ID)
	if gotParent.ParentTaskID != "" {
		t.Fatalf("top-level task got a ParentTaskID: %q", gotParent.ParentTaskID)
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

func TestStorePersistenceFailureDoesNotCommitMemoryState(t *testing.T) {
	db := openTestDB(t)
	s, _ := NewStore(db)
	task, _ := s.Create("sess", "goal", 5)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.Update(task.ID, func(tk *Task) { tk.Summary = "must not commit" }); err == nil {
		t.Fatal("expected persistence failure")
	}
	got, _ := s.Get(task.ID)
	if got.Summary != "" {
		t.Fatalf("in-memory state diverged from failed persistence: %+v", got)
	}
}

func TestRestartStopsChildInsteadOfDoubleResumingIt(t *testing.T) {
	db := openTestDB(t)
	s, _ := NewStore(db)
	parent, _ := s.Create("sess", "parent", 5)
	child, _ := s.CreateSubtask("sess", "child", 5, parent.ID)
	_ = s.Transition(child.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "work", Status: StepPending}}
	})
	_ = s.Transition(child.ID, StatusRunning, nil)

	reloaded, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, _ := reloaded.Get(child.ID)
	if got.Status != StatusStopped || got.Checkpoint.ResumeAfterRestart {
		t.Fatalf("child should be reconciled by parent, not independently resumed: %+v", got)
	}
}
