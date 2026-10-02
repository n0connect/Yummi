package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// StoreExecer runs agent commands through the execution.Store pipeline so they
// appear in the UI and audit trail exactly like chat-approved commands.
type StoreExecer struct {
	store   *execution.Store
	hardCap time.Duration
}

// NewStoreExecer wraps an execution store. hardCap bounds total command time
// (pentest tools can run minutes; default 10m).
func NewStoreExecer(store *execution.Store, hardCap time.Duration) *StoreExecer {
	if hardCap <= 0 {
		hardCap = 10 * time.Minute
	}
	return &StoreExecer{store: store, hardCap: hardCap}
}

// Start enqueues the command and returns a handle to supervise it.
func (e *StoreExecer) Start(ctx context.Context, sessionID, command string) (ExecHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, err := e.store.Enqueue(ctx, sessionID, "", command, "agent_task")
	if err != nil {
		return nil, fmt.Errorf("enqueue: %w", err)
	}
	return &storeHandle{
		store:   e.store,
		recID:   rec.ID,
		command: command,
		start:   time.Now(),
		hardCap: e.hardCap,
	}, nil
}

// Run is a convenience helper: Start + a single bounded wait. It preserves the
// simple path for callers/tests that do not need mid-run supervision.
// A non-done result within hardCap is reported as an infrastructure error.
func (e *StoreExecer) Run(ctx context.Context, sessionID, command string) (ToolResult, error) {
	h, err := e.Start(ctx, sessionID, command)
	if err != nil {
		return ToolResult{}, err
	}
	res, done := h.Wait(e.hardCap)
	if !done {
		h.Cancel()
		if ctx.Err() != nil {
			return ToolResult{}, ctx.Err()
		}
		return ToolResult{}, fmt.Errorf("wait timeout: command did not finish within %s", e.hardCap)
	}
	return res, nil
}

// storeHandle supervises one running command via the execution store.
type storeHandle struct {
	store   *execution.Store
	recID   string
	command string
	start   time.Time
	hardCap time.Duration
}

// Wait blocks up to soft; returns (result, true) when the command reaches a
// terminal state, or (zero, false) if soft elapsed while it is still running.
// Each call starts a fresh bounded poll, so callers may Wait repeatedly to
// keep supervising a long-running command.
func (h *storeHandle) Wait(soft time.Duration) (ToolResult, bool) {
	final, err := h.store.WaitForTerminal(h.recID, soft)
	dur := time.Since(h.start)
	if err != nil {
		// Soft window elapsed while the command is still running.
		// (WaitForTerminal returns a timeout error rather than blocking.)
		return ToolResult{}, false
	}
	return toolResultFromRecord(final, dur), true
}

// toolResultFromRecord converts a terminal execution.Record into the
// ToolResult shape the agent loop consumes — shared by storeHandle.Wait
// (shell commands) and StoreMCPExecer.RunMCPTool (MCP tool calls), which
// otherwise duplicated this exact completed/canceled/failed classification.
func toolResultFromRecord(final execution.Record, dur time.Duration) ToolResult {
	switch final.Status {
	case execution.StatusCompleted:
		return ToolResult{Content: final.Output, Duration: dur}
	case execution.StatusCanceled:
		return ToolResult{Content: orDefault(final.Output, "command aborted"), IsError: true, Duration: dur}
	default: // failed
		return ToolResult{
			Content:  orDefault(final.Error, "execution failed") + "\n" + final.Output,
			IsError:  true,
			Duration: dur,
		}
	}
}

// EgressHold reports whether this execution is currently held awaiting operator
// egress approval, and the target it's blocked on. Returns ("", false) for any
// other state.
func (h *storeHandle) EgressHold() (string, bool) {
	rec, ok := h.store.Get(h.recID)
	if !ok || rec.Status != execution.StatusAwaitingEgressApproval {
		return "", false
	}
	return rec.EgressTarget, true
}

// Cancel requests cancellation of the running command.
func (h *storeHandle) Cancel() { _, _ = h.store.Cancel(h.recID) }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// StoreMCPExecer runs MCP-backed tool calls through the same execution.Store
// pipeline StoreExecer uses for shell commands — same audit trail, gateway
// scope check (via executor.processMCP), and heartbeat, just tagged with the
// tool name and structured arguments instead of a raw command string.
type StoreMCPExecer struct {
	store   *execution.Store
	hardCap time.Duration
}

// NewStoreMCPExecer wraps an execution store. hardCap bounds total tool-call
// time, same default and rationale as NewStoreExecer.
func NewStoreMCPExecer(store *execution.Store, hardCap time.Duration) *StoreMCPExecer {
	if hardCap <= 0 {
		hardCap = 10 * time.Minute
	}
	return &StoreMCPExecer{store: store, hardCap: hardCap}
}

// RunMCPTool enqueues the tool call and blocks for a single bounded wait —
// the same simple synchronous shape StoreExecer.Run offers for the shell
// path (no mid-run supervision needed for these bounded pentest-tool calls).
func (e *StoreMCPExecer) RunMCPTool(ctx context.Context, sessionID, tool string, args map[string]any, displayCommand string) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	rec, err := e.store.EnqueueMCP(ctx, sessionID, "", tool, args, displayCommand, "agent_task")
	if err != nil {
		return ToolResult{}, fmt.Errorf("enqueue mcp: %w", err)
	}
	start := time.Now()
	final, err := e.store.WaitForTerminal(rec.ID, e.hardCap)
	dur := time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return ToolResult{}, ctx.Err()
		}
		return ToolResult{}, fmt.Errorf("wait timeout: mcp tool %s did not finish within %s", tool, e.hardCap)
	}
	return toolResultFromRecord(final, dur), nil
}
