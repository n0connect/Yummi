package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// rememberNode persists a structured memory element (e.g. a source-code
// connection map) to the shared knowledge store under a key, updating it in
// place when the key already exists for the session — the "never-forget"
// persistent memory. It is knowledge-only: no execution path, fail-closed.
// A nil knowledge store degrades to a no-op observation rather than failing.
func rememberNode(ctx context.Context, s *GraphState) (string, error) {
	key := strings.TrimSpace(s.pendingAct.MemKey)
	val := strings.TrimSpace(s.pendingAct.MemValue)
	if key == "" || val == "" {
		s.lastObs = Wrap("TOOL_FAILED", "REMEMBER needs both key and value")
		return edgeOK, nil
	}
	if s.cfg.Knowledge == nil {
		s.lastObs = Wrap("TOOL_RESPONSE", "memory store unavailable; continuing")
		return edgeOK, nil
	}
	task, _ := s.store.Get(s.taskID)
	pc := knowledge.PersistentContext{SessionID: task.SessionID, Key: key, Value: val}
	if existing, ok := s.cfg.Knowledge.FindContext(task.SessionID, key); ok {
		pc.ID = existing.ID
		pc.CreatedAt = existing.CreatedAt
	}
	if err := s.cfg.Knowledge.RecordContext(ctx, pc); err != nil {
		s.lastObs = Wrap("TOOL_FAILED", "failed to remember: "+err.Error())
		return edgeOK, nil
	}
	s.emit(ActContext, key, "")
	s.lastObs = Wrap("TOOL_RESPONSE", fmt.Sprintf("remembered %q (persistent memory updated)", key))
	return edgeOK, nil
}
