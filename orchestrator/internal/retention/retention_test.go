package retention

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// testDB creates a real SQLite database with the minimal subset of the real
// schema (session.Manager / agent.Store own the full versions) needed to
// exercise pruning — no mock of Yummi's own code, matching project
// convention.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	schema := `
		CREATE TABLE chat_messages (id INTEGER PRIMARY KEY, session_id TEXT, created_at INTEGER);
		CREATE TABLE agent_activity (id TEXT PRIMARY KEY, session_id TEXT, timestamp INTEGER);
		CREATE TABLE executions (id TEXT PRIMARY KEY, session_id TEXT, created_at INTEGER);
		CREATE TABLE findings (id TEXT PRIMARY KEY, session_id TEXT, created_at INTEGER);
		CREATE TABLE sessions (id TEXT PRIMARY KEY, created_at INTEGER);
		CREATE TABLE agent_tasks (id TEXT PRIMARY KEY, session_id TEXT, created_at INTEGER);
		CREATE TABLE audit_log (id INTEGER PRIMARY KEY, session_id TEXT, timestamp INTEGER);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestPruneOlderThan_DeletesOnlyRowsOlderThanCutoff(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	mustExec(t, db, "INSERT INTO chat_messages(id, session_id, created_at) VALUES (1, 's1', ?)", old.Unix())
	mustExec(t, db, "INSERT INTO chat_messages(id, session_id, created_at) VALUES (2, 's1', ?)", recent.Unix())
	mustExec(t, db, "INSERT INTO agent_activity(id, session_id, timestamp) VALUES ('a1', 's1', ?)", old.UnixMilli())
	mustExec(t, db, "INSERT INTO agent_activity(id, session_id, timestamp) VALUES ('a2', 's1', ?)", recent.UnixMilli())
	mustExec(t, db, "INSERT INTO executions(id, session_id, created_at) VALUES ('e1', 's1', ?)", old.Unix())
	mustExec(t, db, "INSERT INTO executions(id, session_id, created_at) VALUES ('e2', 's1', ?)", recent.Unix())

	cutoff := now.Add(-24 * time.Hour)
	res := PruneOlderThan(context.Background(), db, cutoff)

	if res.ChatMessages != 1 || res.Activity != 1 || res.Executions != 1 {
		t.Fatalf("Result = %+v, want exactly 1 pruned row per table", res)
	}
	if got := countRows(t, db, "chat_messages"); got != 1 {
		t.Errorf("chat_messages remaining = %d, want 1 (only the recent row)", got)
	}
	if got := countRows(t, db, "agent_activity"); got != 1 {
		t.Errorf("agent_activity remaining = %d, want 1", got)
	}
	if got := countRows(t, db, "executions"); got != 1 {
		t.Errorf("executions remaining = %d, want 1", got)
	}
}

// TestPruneOlderThan_NeverTouchesProtectedTables is the core safety
// invariant this package exists to enforce: findings, sessions, agent_tasks,
// and audit_log must never be deleted by this mechanism, no matter how old
// their rows are — a security tool must not silently destroy compliance/
// attribution-relevant records. See the package doc for the full reasoning.
func TestPruneOlderThan_NeverTouchesProtectedTables(t *testing.T) {
	db := testDB(t)
	ancient := time.Now().Add(-365 * 24 * time.Hour)

	mustExec(t, db, "INSERT INTO findings(id, session_id, created_at) VALUES ('f1', 's1', ?)", ancient.Unix())
	mustExec(t, db, "INSERT INTO sessions(id, created_at) VALUES ('s1', ?)", ancient.Unix())
	mustExec(t, db, "INSERT INTO agent_tasks(id, session_id, created_at) VALUES ('t1', 's1', ?)", ancient.Unix())
	mustExec(t, db, "INSERT INTO audit_log(id, session_id, timestamp) VALUES (1, 's1', ?)", ancient.Unix())

	PruneOlderThan(context.Background(), db, time.Now())

	for _, table := range []string{"findings", "sessions", "agent_tasks", "audit_log"} {
		if got := countRows(t, db, table); got != 1 {
			t.Errorf("%s row count = %d after pruning, want 1 (untouched) — protected table was pruned", table, got)
		}
	}
}

func TestPruneOlderThan_NilDBIsNoop(t *testing.T) {
	res := PruneOlderThan(context.Background(), nil, time.Now())
	if res.Total() != 0 {
		t.Fatalf("expected zero-value Result for nil db, got %+v", res)
	}
}

func TestStartPurger_DisabledByDefault(t *testing.T) {
	db := testDB(t)
	old := time.Now().Add(-48 * time.Hour)
	mustExec(t, db, "INSERT INTO chat_messages(id, session_id, created_at) VALUES (1, 's1', ?)", old.Unix())

	// retentionDays=0 (the default when YUMMI_DATA_RETENTION_DAYS is unset)
	// must not start any goroutine at all.
	StartPurger(t.Context(), db, 0, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	if got := countRows(t, db, "chat_messages"); got != 1 {
		t.Fatalf("chat_messages count = %d, want 1 (untouched — retention disabled)", got)
	}
}

func TestStartPurger_PrunesOnTick(t *testing.T) {
	db := testDB(t)
	old := time.Now().Add(-48 * time.Hour)
	mustExec(t, db, "INSERT INTO chat_messages(id, session_id, created_at) VALUES (1, 's1', ?)", old.Unix())

	StartPurger(t.Context(), db, 1, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countRows(t, db, "chat_messages") == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("StartPurger never pruned the old row within the deadline")
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
