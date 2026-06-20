package conductor

import (
	"testing"
	"time"
)

func TestApprovalStorePutGet(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	proposal := Proposal{
		ID:      "test-1",
		Command: "nmap -sV 192.168.1.1",
	}

	store.Put("session-1", proposal)

	item, err := store.Approve("session-1", "test-1")
	if err != nil {
		t.Fatalf("Failed to approve: %v", err)
	}

	if item.Proposal.ID != "test-1" {
		t.Errorf("Expected proposal ID test-1, got %s", item.Proposal.ID)
	}
	if item.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestApprovalStoreNotFound(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	_, err := store.Approve("session-1", "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent proposal")
	}
}

func TestApprovalStoreExpired(t *testing.T) {
	store := NewApprovalStore(1 * time.Millisecond)

	proposal := Proposal{
		ID:      "test-expired",
		Command: "nmap -sV 192.168.1.1",
	}

	store.Put("session-1", proposal)
	time.Sleep(10 * time.Millisecond)

	_, err := store.Approve("session-1", "test-expired")
	if err == nil {
		t.Error("Expected error for expired proposal")
	}
}

func TestApprovalStorePurge(t *testing.T) {
	store := NewApprovalStore(1 * time.Millisecond)

	for i := 0; i < 5; i++ {
		store.Put("session-1", Proposal{ID: string(rune('a' + i))})
	}

	time.Sleep(10 * time.Millisecond)

	purged := store.PurgeExpired()
	if purged == 0 {
		t.Error("Expected some approvals to be purged")
	}
}

func TestApprovalStoreMultipleSessions(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	store.Put("session-1", Proposal{ID: "prop-1"})
	store.Put("session-2", Proposal{ID: "prop-2"})

	_, err := store.Approve("session-1", "prop-1")
	if err != nil {
		t.Fatalf("Failed to approve session-1: %v", err)
	}

	_, err = store.Approve("session-2", "prop-2")
	if err != nil {
		t.Fatalf("Failed to approve session-2: %v", err)
	}
}

func TestApprovalStoreNilStore(t *testing.T) {
	var store *ApprovalStore

	store.Put("s1", Proposal{ID: "p1"})

	_, err := store.Approve("s1", "p1")
	if err == nil {
		t.Error("Expected error for nil store")
	}

	purged := store.PurgeExpired()
	if purged != 0 {
		t.Error("Nil store should return 0 purged")
	}
}

// TestApprovalStoreList verifies that List only returns proposals for the
// requested session, skipping other sessions and expired entries.
func TestApprovalStoreList(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	store.Put("session-a", Proposal{ID: "a1"})
	store.Put("session-a", Proposal{ID: "a2"})
	store.Put("session-b", Proposal{ID: "b1"})

	listA := store.List("session-a")
	if len(listA) != 2 {
		t.Errorf("expected 2 proposals for session-a, got %d", len(listA))
	}
	for _, p := range listA {
		if p.SessionID != "session-a" {
			t.Errorf("List leaked session-b proposal: %s", p.SessionID)
		}
	}

	listB := store.List("session-b")
	if len(listB) != 1 {
		t.Errorf("expected 1 proposal for session-b, got %d", len(listB))
	}

	empty := store.List("session-none")
	if len(empty) != 0 {
		t.Errorf("expected empty list for unknown session, got %d", len(empty))
	}

	// Nil receiver must be safe.
	var nilStore *ApprovalStore
	if out := nilStore.List("x"); out != nil {
		t.Errorf("expected nil from nil store, got %v", out)
	}
}

// TestApprovalStoreDoublePut writing the same (session, proposal ID) twice
// should keep exactly one live entry (the second overwrites the first).
func TestApprovalStoreDoublePut(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	store.Put("s1", Proposal{ID: "dup", Command: "v1"})
	store.Put("s1", Proposal{ID: "dup", Command: "v2"})

	list := store.List("s1")
	if len(list) != 1 {
		t.Fatalf("expected 1 entry after double put, got %d", len(list))
	}
	if list[0].Proposal.Command != "v2" {
		t.Errorf("expected the second Put to win, got command=%q", list[0].Proposal.Command)
	}
}

// TestApprovalStorePurgeMixed verifies Purge only removes expired entries
// while leaving live ones intact. Expired entries are injected by fabricating
// pending proposals with past ExpiresAt timestamps.
func TestApprovalStorePurgeMixed(t *testing.T) {
	store := NewApprovalStore(20 * time.Minute)

	// Live entry via the normal path.
	store.Put("s1", Proposal{ID: "live"})

	// Inject an expired entry directly into the underlying map so we don't
	// need to sleep in tests.
	store.mu.Lock()
	store.items["s1:expired"] = PendingProposal{
		SessionID: "s1",
		Proposal:  Proposal{ID: "expired"},
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	store.mu.Unlock()

	purged := store.Purge()
	if purged != 1 {
		t.Errorf("expected to purge exactly 1 expired entry, got %d", purged)
	}

	// Live entry must still be approvable.
	if _, err := store.Approve("s1", "live"); err != nil {
		t.Errorf("live entry should remain approvable after purge, got %v", err)
	}
}
