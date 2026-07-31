package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DelegationRun is the operator-visible lifecycle of one specialist run.
// The coordinator keeps completed entries as a bounded recent history so a
// fan-out is inspectable after it finishes, not only while goroutines exist.
type DelegationRun struct {
	ID           string `json:"id"`
	ParentTaskID string `json:"parent_task_id"`
	TaskID       string `json:"task_id,omitempty"`
	Role         string `json:"role"`
	Subgoal      string `json:"subgoal"`
	Status       string `json:"status"` // running | completed | failed | interrupted
	Error        string `json:"error,omitempty"`
	StartedAt    int64  `json:"started_at"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
}

type delegationEntry struct {
	run    DelegationRun
	cancel context.CancelFunc
}

// DelegationCoordinator globally bounds specialist concurrency for a Runner,
// tracks addressable child lifecycles, and supports individual interruption.
// One instance is shared by every graph state created by the top-level Runner.
type DelegationCoordinator struct {
	mu      sync.RWMutex
	sem     chan struct{}
	runs    map[string]delegationEntry
	history int
}

func NewDelegationCoordinator(maxConcurrent int) *DelegationCoordinator {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &DelegationCoordinator{
		sem: make(chan struct{}, maxConcurrent), runs: make(map[string]delegationEntry), history: 200,
	}
}

// Begin waits for a global execution slot and registers an addressable run.
func (c *DelegationCoordinator) Begin(ctx context.Context, parentTaskID, role, subgoal string) (string, context.Context, error) {
	if c == nil {
		return "", nil, fmt.Errorf("delegation coordinator unavailable")
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	childCtx, cancel := context.WithCancel(ctx)
	id := uuid.NewString()
	c.mu.Lock()
	c.runs[id] = delegationEntry{run: DelegationRun{
		ID: id, ParentTaskID: parentTaskID, Role: role, Subgoal: subgoal,
		Status: "running", StartedAt: time.Now().Unix(),
	}, cancel: cancel}
	c.mu.Unlock()
	return id, childCtx, nil
}

// BindTask connects the coordinator run to the persisted child task as soon as
// planning creates it, allowing the operator to navigate both identities.
func (c *DelegationCoordinator) BindTask(id, taskID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	entry, ok := c.runs[id]
	if ok {
		entry.run.TaskID = taskID
		c.runs[id] = entry
	}
	c.mu.Unlock()
}

// Finish releases the global slot exactly once and records the terminal state.
func (c *DelegationCoordinator) Finish(id string, err error) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	entry, ok := c.runs[id]
	if !ok || entry.run.FinishedAt != 0 {
		c.mu.Unlock()
		return
	}
	entry.cancel()
	entry.run.FinishedAt = time.Now().Unix()
	if entry.run.Status == "interrupted" {
		// Preserve the explicit operator action even when the child returns
		// context.Canceled a moment later.
	} else if err != nil {
		entry.run.Status = "failed"
		entry.run.Error = err.Error()
	} else {
		entry.run.Status = "completed"
	}
	entry.cancel = nil
	c.runs[id] = entry
	c.pruneLocked()
	c.mu.Unlock()
	<-c.sem
}

func (c *DelegationCoordinator) Interrupt(id string) error {
	if c == nil {
		return fmt.Errorf("delegation coordinator unavailable")
	}
	c.mu.Lock()
	entry, ok := c.runs[id]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("delegation %s not found", id)
	}
	if entry.run.FinishedAt != 0 || entry.cancel == nil {
		c.mu.Unlock()
		return fmt.Errorf("delegation %s is already %s", id, entry.run.Status)
	}
	entry.run.Status = "interrupted"
	entry.run.Error = "interrupted by operator"
	cancel := entry.cancel
	c.runs[id] = entry
	c.mu.Unlock()
	cancel()
	return nil
}

func (c *DelegationCoordinator) List() []DelegationRun {
	if c == nil {
		return []DelegationRun{}
	}
	c.mu.RLock()
	out := make([]DelegationRun, 0, len(c.runs))
	for _, entry := range c.runs {
		out = append(out, entry.run)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

func (c *DelegationCoordinator) pruneLocked() {
	if len(c.runs) <= c.history {
		return
	}
	type finished struct {
		id string
		at int64
	}
	var rows []finished
	for id, entry := range c.runs {
		if entry.run.FinishedAt != 0 {
			rows = append(rows, finished{id: id, at: entry.run.FinishedAt})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at < rows[j].at })
	for len(c.runs) > c.history && len(rows) > 0 {
		delete(c.runs, rows[0].id)
		rows = rows[1:]
	}
}
