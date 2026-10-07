package eino

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver used across orchestrator (session/execution)

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// buildResumeEvidenceDigest reads from the durable agent_activity table
// (Store.ListActivity returns nothing without a real DB behind it), so these
// tests need an actual in-memory sqlite connection — not agent.NewStore(nil),
// which is for tests that only touch the in-process task map.
func openTestStore(t *testing.T) *agent.Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // see agent.openTestDB's own doc comment for why
	t.Cleanup(func() { db.Close() })
	store, err := agent.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// Regression test for the live-observed bug: an operator granting more steps
// after a budget pause watched the resumed model redo recon from zero,
// because the resumed conversation carried only the original goal plus the
// terse pause Q&A — nothing about the commands already run. The fix rebuilds
// a digest from the durable agent_activity log; this locks that behavior in.
func TestBuildResumeEvidenceDigest(t *testing.T) {
	store := openTestStore(t)
	task, err := store.Create("session-1", "compromise the target", 15)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: task.ID,
		Kind: agent.ActCommand, Summary: "fingerprint the server", Detail: "whatweb -a 3 http://198.51.100.10",
	})
	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: task.ID,
		Kind: agent.ActCommandDone, Detail: "Apache/2.4.49 (Unix)",
	})
	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: task.ID,
		Kind: agent.ActCommand, Summary: "exploit the path traversal RCE", Detail: "curl --path-as-is ... /bin/sh -d id",
	})
	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: task.ID,
		Kind: agent.ActCommandDone, Detail: "uid=1(daemon) gid=1(daemon) groups=1(daemon)",
	})

	digest := buildResumeEvidenceDigest(store, "session-1", task.ID)

	for _, want := range []string{
		"whatweb -a 3 http://198.51.100.10",
		"Apache/2.4.49",
		"/bin/sh",
		"uid=1(daemon)",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest missing %q:\n%s", want, digest)
		}
	}
}

func TestBuildResumeEvidenceDigestEmptyWhenNoActivity(t *testing.T) {
	store := openTestStore(t)
	task, _ := store.Create("session-1", "goal", 15)
	if digest := buildResumeEvidenceDigest(store, "session-1", task.ID); digest != "" {
		t.Errorf("expected empty digest for a task with no recorded activity, got:\n%s", digest)
	}
}

// A sibling task's commands in the same session must never leak into this
// task's digest — ListActivity is session-scoped, so the filter by TaskID is
// what keeps tasks isolated.
func TestBuildResumeEvidenceDigestIsolatesOtherTasksInSameSession(t *testing.T) {
	store := openTestStore(t)
	taskA, _ := store.Create("session-1", "goal A", 15)
	taskB, _ := store.Create("session-1", "goal B", 15)

	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: taskB.ID,
		Kind: agent.ActCommand, Summary: "unrelated", Detail: "nmap -p 1-65535 10.0.0.1",
	})
	store.RecordActivity(agent.ActivityEvent{
		SessionID: "session-1", TaskID: taskB.ID,
		Kind: agent.ActCommandDone, Detail: "all ports closed",
	})

	if digest := buildResumeEvidenceDigest(store, "session-1", taskA.ID); digest != "" {
		t.Errorf("task A's digest must not contain task B's activity, got:\n%s", digest)
	}
}
