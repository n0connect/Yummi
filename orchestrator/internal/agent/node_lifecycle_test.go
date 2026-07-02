package agent

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// finishState builds a minimal GraphState for exercising finishNode directly
// (bypassing the full step loop, which isn't needed to test the terminal
// transition + the async skill-curator hook).
func finishState(t *testing.T, store *Store, task Task, cfg Config, outcome finishOutcome, msg string) *GraphState {
	t.Helper()
	return &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           cfg,
		gate:          NewGate(),
		finishOutcome: outcome,
		finishMsg:     msg,
		memCtx:        MemoryContext{TokenBudget: 6000, Observations: []string{"nmap found port 22 open"}},
	}
}

func TestFinishNodeFiresSkillCuratorOnCompletedOnly(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeCompleted, "recon complete")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		if traj.TaskID != task.ID || traj.SessionID != task.SessionID {
			t.Fatalf("unexpected trajectory: %+v", traj)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected SkillCurator to fire on task completion")
	}
}

func TestFinishNodeDoesNotFireSkillCuratorOnFailure(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeFailed, "boom")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		t.Fatalf("SkillCurator must not fire on a failed task, got %+v", traj)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing fired
	}
}

func TestFinishNodeDoesNotFireSkillCuratorOnStopped(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeStopped, "operator stopped")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		t.Fatalf("SkillCurator must not fire on a stopped task, got %+v", traj)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestFinishNodeSkillCuratorDoesNotBlockReturn proves the hook is fired async
// (a slow/hanging curator must never delay finishNode's return to the caller).
func TestFinishNodeSkillCuratorDoesNotBlockReturn(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	release := make(chan struct{})
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, _ skillcurator.Trajectory) { <-release }
	defer close(release)

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeCompleted, "done")

	done := make(chan struct{})
	go func() {
		_, _ = finishNode(context.Background(), s)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("finishNode blocked on a slow SkillCurator callback")
	}
}

func TestFinishNodeNilSkillCuratorIsNoop(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, fastRetryCfg(), outcomeCompleted, "done") // SkillCurator left nil
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode with nil SkillCurator: %v", err)
	}
}
