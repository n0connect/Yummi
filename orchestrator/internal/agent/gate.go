package agent

import (
	"fmt"
	"sync"
	"time"
)

// Verdict is the operator's decision on a gated command.
type Verdict struct {
	Approved bool
	Reason   string
}

// PendingApproval describes the command a task is currently blocked on.
type PendingApproval struct {
	TaskID     string `json:"task_id"`
	ProposalID string `json:"proposal_id"`
	Command    string `json:"command"`
	RiskLevel  string `json:"risk_level"`
	Version    int    `json:"version"` // incremented per Request; stale Resolves are rejected
	CreatedAt  int64  `json:"created_at"`
}

type pendingEntry struct {
	info      PendingApproval
	ch        chan Verdict
	pendingAt time.Time
}

// Gate blocks the task loop on medium+ risk commands until the operator
// resolves them. One pending approval per task (the loop is sequential).
type Gate struct {
	mu      sync.Mutex
	pending map[string]pendingEntry // keyed by taskID
	version map[string]int          // monotonic version counter per taskID
	ttl     time.Duration
}

// NewGate creates an empty approval gate and starts the TTL purge goroutine.
func NewGate() *Gate {
	g := &Gate{
		pending: make(map[string]pendingEntry),
		version: make(map[string]int),
		ttl:     20 * time.Minute,
	}
	go g.purge()
	return g
}

// Request registers a pending approval and returns the verdict channel.
// A second Request for the same task replaces the first (stale loop safety).
// Version is incremented on each Request so stale Resolve calls are rejected.
func (g *Gate) Request(taskID, proposalID, command, riskLevel string) <-chan Verdict {
	ch := make(chan Verdict, 1)
	g.mu.Lock()
	g.version[taskID]++
	ver := g.version[taskID]
	g.pending[taskID] = pendingEntry{
		info: PendingApproval{
			TaskID:     taskID,
			ProposalID: proposalID,
			Command:    command,
			RiskLevel:  riskLevel,
			Version:    ver,
			CreatedAt:  time.Now().Unix(),
		},
		ch:        ch,
		pendingAt: time.Now(),
	}
	g.mu.Unlock()
	return ch
}

// Resolve delivers the operator's verdict to the waiting loop.
// Returns an error if the proposalID is stale (already replaced by a new Request).
func (g *Gate) Resolve(taskID, proposalID string, approved bool, reason string) error {
	g.mu.Lock()
	entry, ok := g.pending[taskID]
	if !ok || entry.info.ProposalID != proposalID {
		g.mu.Unlock()
		return fmt.Errorf("no pending approval for task %s proposal %s", taskID, proposalID)
	}
	delete(g.pending, taskID)
	g.mu.Unlock()
	entry.ch <- Verdict{Approved: approved, Reason: reason}
	return nil
}

// Pending returns the approval a task is currently blocked on, if any.
func (g *Gate) Pending(taskID string) (PendingApproval, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.pending[taskID]
	return entry.info, ok
}

// Clear drops a pending approval without a verdict (task stopped / timed out).
func (g *Gate) Clear(taskID string) {
	g.mu.Lock()
	delete(g.pending, taskID)
	g.mu.Unlock()
}

// purge runs forever, evicting entries that have exceeded the TTL.
// Evicted entries are silently dropped (the loop already handles timeout via select).
func (g *Gate) purge() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		g.mu.Lock()
		for taskID, entry := range g.pending {
			if now.Sub(entry.pendingAt) > g.ttl {
				delete(g.pending, taskID)
			}
		}
		g.mu.Unlock()
	}
}
