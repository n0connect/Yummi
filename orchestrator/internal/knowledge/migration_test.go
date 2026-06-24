package knowledge

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestMigratesPreProvenanceFindingsTable simulates an existing database created
// by the OLD session schema (findings without source/task_id columns) and
// verifies NewStore migrates it in place without data loss, defaulting old
// rows' source to "manual".
func TestMigratesPreProvenanceFindingsTable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	// Old-schema findings table (pre-provenance), as session used to create it.
	_, err = db.Exec(`CREATE TABLE findings (
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL, target_id TEXT,
		severity TEXT NOT NULL, title TEXT NOT NULL, description TEXT,
		evidence TEXT, created_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatalf("old schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO findings(id,session_id,severity,title,created_at) VALUES('f0','s1','medium','legacy finding',1)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// NewStore must ALTER the table and preserve the existing row.
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore migration: %v", err)
	}

	got := s.ListFindings("s1")
	if len(got) != 1 {
		t.Fatalf("legacy row lost during migration, got %d findings", len(got))
	}
	if got[0].Title != "legacy finding" || got[0].Source != "manual" {
		t.Fatalf("legacy row not migrated correctly: %+v", got[0])
	}

	// New writes with provenance must also work post-migration.
	if err := s.RecordFinding(context.Background(), Finding{
		SessionID: "s1", Severity: "high", Title: "new agent finding", Source: "agent", TaskID: "t9",
	}); err != nil {
		t.Fatalf("post-migration write: %v", err)
	}
	if len(s.ListFindings("s1")) != 2 {
		t.Fatal("post-migration write not persisted")
	}
}
