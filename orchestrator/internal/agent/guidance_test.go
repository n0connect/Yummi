package agent

import "testing"

// TestAppendDrainGuidance exercises the mid-task operator-steering queue
// (Task.PendingGuidance): a chat message sent while a task is already
// running must reach the task's next turn exactly once, never lost, never
// duplicated. See handlers_chat.go's findActiveTask/steerableTaskStatus for
// the routing side and node_step.go/pentest_tools.go's runToolPipeline for
// the two backends' delivery points.
func TestAppendDrainGuidance(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	task, err := store.Create("session-1", "scan the target", 20)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Draining with nothing queued is a no-op, not an error.
	if got := store.DrainGuidance(task.ID); got != nil {
		t.Fatalf("DrainGuidance with nothing queued = %v, want nil", got)
	}

	if err := store.AppendGuidance(task.ID, "focus on port 80 only"); err != nil {
		t.Fatalf("AppendGuidance: %v", err)
	}
	if err := store.AppendGuidance(task.ID, "skip the privesc stage for now"); err != nil {
		t.Fatalf("AppendGuidance: %v", err)
	}

	got := store.DrainGuidance(task.ID)
	want := []string{"focus on port 80 only", "skip the privesc stage for now"}
	if len(got) != len(want) {
		t.Fatalf("DrainGuidance = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DrainGuidance[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Drained once — must not come back on a second drain (no duplicate
	// delivery) and must not resurface via a later Get().
	if got := store.DrainGuidance(task.ID); got != nil {
		t.Fatalf("second DrainGuidance = %v, want nil (already delivered)", got)
	}
	reloaded, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("Get after drain: task not found")
	}
	if len(reloaded.PendingGuidance) != 0 {
		t.Fatalf("PendingGuidance after drain = %v, want empty", reloaded.PendingGuidance)
	}
}

func TestAppendGuidanceUnknownTask(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.AppendGuidance("no-such-task", "hello"); err == nil {
		t.Fatal("AppendGuidance on an unknown task id should error")
	}
}
