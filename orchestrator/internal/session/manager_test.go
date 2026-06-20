package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewManager(db)
}

func TestSessionCRUD(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()

	s := &Session{
		ID: "test-1", Name: "Test Engagement",
		CreatedAt: now, UpdatedAt: now,
		Status: "active", GatewayLevel: 0,
	}

	// Create
	if err := sm.Create(s); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get
	got, err := sm.Get("test-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Test Engagement" {
		t.Fatalf("name mismatch: %s", got.Name)
	}

	// List
	sessions, err := sm.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	// Update
	got.Name = "Updated"
	got.UpdatedAt = now + 1
	if err := sm.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := sm.Get("test-1")
	if got2.Name != "Updated" {
		t.Fatalf("update failed: %s", got2.Name)
	}

	// Delete (soft)
	if err := sm.Delete("test-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got3, _ := sm.Get("test-1")
	if got3.Status != "closed" {
		t.Fatalf("delete failed: status=%s", got3.Status)
	}
}

func TestTargetCRUD(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()

	sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"})

	target := &Target{
		ID: "t1", SessionID: "s1", Value: "192.168.1.1",
		InScope: true, Notes: "web server", CreatedAt: now,
	}
	if err := sm.CreateTarget(target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	targets, err := sm.ListTargets("s1")
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Value != "192.168.1.1" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
}

func TestFindingCRUD(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()

	sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"})

	finding := &Finding{
		ID: "f1", SessionID: "s1", Severity: "high",
		Title: "SQL Injection", Description: "Found in login form",
		CreatedAt: now,
	}
	if err := sm.CreateFinding(finding); err != nil {
		t.Fatalf("create finding: %v", err)
	}

	findings, err := sm.ListFindings("s1")
	if err != nil {
		t.Fatalf("list findings: %v", err)
	}
	if len(findings) != 1 || findings[0].Title != "SQL Injection" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestOpenDBCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "deep", "sessions.db")
	os.MkdirAll(filepath.Dir(dbPath), 0o755)

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db in nested dir: %v", err)
	}
	db.Close()
}

func TestAuditLog(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()
	if err := sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := sm.AppendAudit("s1", "assistant.chat", "assistant", map[string]any{"intent": "suggest_command"}); err != nil {
		t.Fatalf("append audit #1: %v", err)
	}
	if err := sm.AppendAudit("s1", "assistant.proposal.approved", "user", map[string]any{"proposal_id": "p1"}); err != nil {
		t.Fatalf("append audit #2: %v", err)
	}

	events, err := sm.ListAudit("s1", 10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType == "" || events[0].Actor == "" {
		t.Fatalf("expected non-empty event fields: %+v", events[0])
	}
}
