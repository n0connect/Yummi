package skillcurator

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProposeFromTaskCleanDraftBecomesPending(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"web-admin-panel-discovery","description":"find hidden admin panels",
		"triggers":["admin panel"],"tools":["gobuster"],"knowledge":"run gobuster dir against the target with a common wordlist"}`, nil
	}
	c := NewCurator(store, chat)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, err := store.List(context.Background(), "pending")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending draft, got %d", len(pending))
	}
	ps := pending[0]
	if ps.Name != "web-admin-panel-discovery" || ps.Origin != "auto" || ps.TaskID != "t1" {
		t.Fatalf("unexpected pending row: %+v", ps)
	}
	if !strings.Contains(ps.DraftYAML, "web-admin-panel-discovery") {
		t.Fatalf("expected draft_yaml to contain the skill name, got %q", ps.DraftYAML)
	}
	if ps.ThreatScanResult != "" {
		t.Fatalf("expected a clean scan result, got %q", ps.ThreatScanResult)
	}
}

// TestProposeFromTaskThreatFlaggedDraftIsRejectedNotQueued is the core safety
// guarantee: a draft that trips the SAME threatscan check skill.LoadDir
// applies to hand-authored skills must NEVER reach the pending queue an
// operator reviews — it's rejected outright, with an audit trail.
func TestProposeFromTaskThreatFlaggedDraftIsRejectedNotQueued(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"ssh-persistence","description":"establish access",
		"triggers":["ssh"],"tools":["ssh"],"knowledge":"append your key to ~/.ssh/authorized_keys for persistence"}`, nil
	}
	c := NewCurator(store, chat)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	if len(pending) != 0 {
		t.Fatalf("expected the flagged draft to NEVER reach the pending queue, got %+v", pending)
	}
	rejected, err := store.List(context.Background(), "rejected")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rejected) != 1 {
		t.Fatalf("expected 1 rejected (audited) draft, got %d", len(rejected))
	}
	if rejected[0].ThreatScanResult == "" {
		t.Fatal("expected a non-empty threat_scan_result on the rejected row (audit trail)")
	}
}

func TestProposeFromTaskChatErrorWritesNoRow(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chat := func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("ollama unreachable")
	}
	c := NewCurator(store, chat)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	rejected, _ := store.List(context.Background(), "rejected")
	if len(pending)+len(rejected) != 0 {
		t.Fatalf("expected no row written on a chat error, got pending=%+v rejected=%+v", pending, rejected)
	}
}

func TestProposeFromTaskNilCuratorDoesNotPanic(t *testing.T) {
	var c *Curator
	c.ProposeFromTask(context.Background(), sampleTrajectory()) // must not panic
}
