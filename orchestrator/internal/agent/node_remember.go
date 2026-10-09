package agent

import (
	"context"
)

// rememberNode is the graph engine's REMEMBER / forget step. The note policy
// (threat scan, provenance, capacity, forgetting) lives in ApplyNote, shared
// with the Eino backend, so the two engines cannot drift apart. A nil knowledge
// store degrades to a no-op observation rather than failing.
func rememberNode(ctx context.Context, s *GraphState) (string, error) {
	task, _ := s.store.Get(s.taskID)
	origin := "agent"
	if s.parentTaskID != "" {
		origin = "subagent" // a delegated sub-agent wrote this memory
	}
	out := ApplyNote(ctx, s.cfg.Knowledge, task.SessionID, origin, s.pendingAct.MemKey, s.pendingAct.MemValue)
	if out.OK && s.cfg.Knowledge != nil && s.pendingAct.MemValue != "" {
		s.emit(ActContext, s.pendingAct.MemKey, "")
	} else if out.Blocked {
		s.emit(ActContextRejected, out.Message, "")
	}
	if out.OK {
		s.lastObs = Wrap("TOOL_RESPONSE", out.Message)
	} else {
		s.lastObs = Wrap("TOOL_FAILED", out.Message)
	}
	return edgeOK, nil
}
