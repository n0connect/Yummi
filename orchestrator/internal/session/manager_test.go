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

// Target and finding persistence moved to internal/knowledge; see that
// package's tests. session retains sessions, audit, and executions only.

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

func TestChatMessagesPersistAndOrder(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()
	if err := sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := sm.AppendMessage("s1", "user", "scan the host", ""); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := sm.AppendMessage("s1", "assistant", "Running nmap.", `{"intent":"suggest_command"}`); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := sm.AppendMessage("s1", "user", "what did it find?", ""); err != nil {
		t.Fatalf("append user 2: %v", err)
	}

	msgs, err := sm.ListMessages("s1", 100)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Chronological order: oldest first.
	if msgs[0].Content != "scan the host" || msgs[0].Role != "user" {
		t.Fatalf("first message wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Payload != `{"intent":"suggest_command"}` {
		t.Fatalf("assistant payload not preserved: %+v", msgs[1])
	}
	if msgs[2].Content != "what did it find?" {
		t.Fatalf("last message wrong: %+v", msgs[2])
	}

	// A different session must not see s1's messages.
	other, err := sm.ListMessages("s2", 100)
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("session isolation broken: %+v", other)
	}
}

// TestChatMessagesReturnsTailInOrder proves the limit keeps the most recent
// turns (the tail of a long conversation) but still hands them back oldest-first.
func TestChatMessagesReturnsTailInOrder(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()
	_ = sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"})

	for i := range 10 {
		if err := sm.AppendMessage("s1", "user", string(rune('a'+i)), ""); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	msgs, err := sm.ListMessages("s1", 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
	// Newest three are h,i,j — returned oldest-first.
	if msgs[0].Content != "h" || msgs[1].Content != "i" || msgs[2].Content != "j" {
		t.Fatalf("tail order wrong: %q %q %q", msgs[0].Content, msgs[1].Content, msgs[2].Content)
	}
}

func TestChatMessageContentCapped(t *testing.T) {
	sm := testDB(t)
	now := time.Now().Unix()
	_ = sm.Create(&Session{ID: "s1", Name: "S1", CreatedAt: now, UpdatedAt: now, Status: "active"})

	huge := make([]byte, maxChatContentBytes+5000)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := sm.AppendMessage("s1", "user", string(huge), ""); err != nil {
		t.Fatalf("append huge: %v", err)
	}
	msgs, _ := sm.ListMessages("s1", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) > maxChatContentBytes {
		t.Fatalf("content not capped: %d bytes", len(msgs[0].Content))
	}
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
