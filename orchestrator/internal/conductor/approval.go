package conductor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PendingProposal represents a proposal waiting for user approval.
type PendingProposal struct {
	SessionID string    `json:"session_id"`
	Proposal  Proposal  `json:"proposal"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ApprovalStore keeps approval-required proposals in memory.
type ApprovalStore struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]PendingProposal
}

// NewApprovalStore creates a new in-memory proposal approval store.
func NewApprovalStore(ttl time.Duration) *ApprovalStore {
	if ttl <= 0 {
		ttl = 20 * time.Minute
	}
	return &ApprovalStore{
		ttl:   ttl,
		items: make(map[string]PendingProposal),
	}
}

// Put saves a proposal under a session.
func (s *ApprovalStore) Put(sessionID string, proposal Proposal) {
	if s == nil || proposal.ID == "" {
		return
	}
	now := time.Now().UTC()
	pending := PendingProposal{
		SessionID: sessionID,
		Proposal:  proposal,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	s.items[s.key(sessionID, proposal.ID)] = pending
	s.mu.Unlock()
}

// Approve marks a proposal approved and removes it from the pending store.
func (s *ApprovalStore) Approve(sessionID, proposalID string) (PendingProposal, error) {
	if s == nil {
		return PendingProposal{}, fmt.Errorf("approval store not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.key(sessionID, proposalID)
	item, ok := s.items[k]
	if !ok {
		return PendingProposal{}, fmt.Errorf("proposal not found")
	}
	if time.Now().UTC().After(item.ExpiresAt) {
		delete(s.items, k)
		return PendingProposal{}, fmt.Errorf("proposal expired")
	}
	delete(s.items, k)
	return item, nil
}

// PurgeExpired removes expired proposals from memory.
func (s *ApprovalStore) PurgeExpired() int {
	if s == nil {
		return 0
	}
	now := time.Now().UTC()
	removed := 0

	s.mu.Lock()
	for k, v := range s.items {
		if now.After(v.ExpiresAt) {
			delete(s.items, k)
			removed++
		}
	}
	s.mu.Unlock()

	return removed
}

// Purge is an alias for PurgeExpired; kept for API symmetry with List.
func (s *ApprovalStore) Purge() int {
	return s.PurgeExpired()
}

// List returns all unexpired pending proposals for the given session.
// Returns an empty slice when the store is nil or no match is found.
func (s *ApprovalStore) List(sessionID string) []PendingProposal {
	if s == nil {
		return nil
	}
	now := time.Now().UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]PendingProposal, 0)
	for _, v := range s.items {
		if v.SessionID != sessionID {
			continue
		}
		if now.After(v.ExpiresAt) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func (s *ApprovalStore) key(sessionID, proposalID string) string {
	return sessionID + ":" + proposalID
}

// StartPurger runs a background goroutine that calls PurgeExpired on the given
// interval. It stops when ctx is cancelled. Call this once from main.
func (s *ApprovalStore) StartPurger(ctx context.Context, interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n := s.PurgeExpired()
				if n > 0 {
					slog.Info("approval store: purged expired proposals", "count", n)
				}
			}
		}
	}()
}
