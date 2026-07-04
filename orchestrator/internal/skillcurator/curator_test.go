package skillcurator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/skill"
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
	c := NewCurator(store, chat, nil)
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
	c := NewCurator(store, chat, nil)
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

// TestProposeFromTaskThreatInNameFieldIsRejected proves the scan covers the
// drafted Name, not just Description/Knowledge — Name is rendered verbatim
// into the agent's step prompt by skill.Registry.Render, so it's an equally
// valid injection vector as Knowledge.
func TestProposeFromTaskThreatInNameFieldIsRejected(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"append-to-authorized_keys-backdoor","description":"helper",
		"triggers":["helper"],"tools":["ssh"],"knowledge":"run a simple recon step"}`, nil
	}
	c := NewCurator(store, chat, nil)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	if len(pending) != 0 {
		t.Fatalf("expected threat pattern in Name to be caught, got pending=%+v", pending)
	}
	rejected, _ := store.List(context.Background(), "rejected")
	if len(rejected) != 1 {
		t.Fatalf("expected 1 rejected (audited) draft, got %d", len(rejected))
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
	c := NewCurator(store, chat, nil)
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

// TestProposeFromTaskSkipsDuplicateOfExistingRegistrySkill proves the curator
// doesn't spam the operator's review queue with a skill that's already live —
// a candidate sharing a Tool AND a Trigger with a skill already in the loaded
// registry is a near-duplicate and must not become a new pending row.
func TestProposeFromTaskSkipsDuplicateOfExistingRegistrySkill(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reg := skill.New([]skill.Skill{
		{Name: "web-admin-panel-discovery", Triggers: []string{"admin panel"}, Tools: []string{"gobuster"}},
	})
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"find-hidden-admin-panels","description":"locate admin consoles",
		"triggers":["admin panel"],"tools":["gobuster"],"knowledge":"brute-force common admin paths"}`, nil
	}
	c := NewCurator(store, chat, reg)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	if len(pending) != 0 {
		t.Fatalf("expected a near-duplicate of an existing registry skill to be skipped, got %+v", pending)
	}
}

// TestProposeFromTaskSkipsDuplicateOfExistingPendingDraft proves the check also
// covers drafts still awaiting review, not just already-approved skills — two
// tasks completing similar work back to back must not both queue the same
// idea for the operator to review twice.
func TestProposeFromTaskSkipsDuplicateOfExistingPendingDraft(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Put(context.Background(), PendingSkill{
		TaskID: "earlier-task", SessionID: "s1", Name: "recon-smb-shares", Status: "pending",
		DraftYAML: "name: recon-smb-shares\ntriggers: [\"smb\"]\ntools: [\"smbclient\"]\nknowledge: enumerate shares\n",
	}); err != nil {
		t.Fatalf("seed pending draft: %v", err)
	}
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"enumerate-smb-shares","description":"list smb shares",
		"triggers":["smb"],"tools":["smbclient"],"knowledge":"run smbclient -L against the target"}`, nil
	}
	c := NewCurator(store, chat, nil)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	if len(pending) != 1 {
		t.Fatalf("expected only the pre-seeded draft (new near-duplicate skipped), got %d: %+v", len(pending), pending)
	}
}

// TestProposeFromTaskAllowsDistinctSkillDespiteSharedTool proves the dedup
// check isn't so aggressive it blocks genuinely different skills that merely
// happen to use the same tool — sharing a Tool alone (no shared Trigger) must
// still be queued.
func TestProposeFromTaskAllowsDistinctSkillDespiteSharedTool(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reg := skill.New([]skill.Skill{
		{Name: "nmap-service-version", Triggers: []string{"version detection"}, Tools: []string{"nmap"}},
	})
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"nmap-udp-sweep","description":"sweep udp ports",
		"triggers":["udp scan"],"tools":["nmap"],"knowledge":"run nmap -sU against the target"}`, nil
	}
	c := NewCurator(store, chat, reg)
	c.ProposeFromTask(context.Background(), sampleTrajectory())

	pending, _ := store.List(context.Background(), "pending")
	if len(pending) != 1 {
		t.Fatalf("expected a genuinely distinct skill to still be queued, got %d", len(pending))
	}
}
