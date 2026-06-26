package agent

import (
	"context"
	"fmt"
	"strings"
)

// handoffNode delegates a focused subtask to a specialized sub-agent and feeds
// its result back to the parent loop as the next observation. The sub-agent
// shares the Knowledge Store, so its findings join the one world model; only a
// human-readable summary is threaded back into the parent's prompt. It always
// returns edgeOK → back to stepNode: success or failure is conveyed in the
// observation, never as a graph error, so a failed handoff can't wedge the loop.
//
// Budget was already consumed for this turn by stepNode; the sub-agent runs on
// its own independent step budget. Recursion is impossible because sub-agents
// are built with an empty SubAgents map (handoff depth is capped at 1).
func handoffNode(ctx context.Context, s *GraphState) (string, error) {
	role := strings.TrimSpace(s.pendingAct.Role)
	subgoal := strings.TrimSpace(s.pendingAct.Subgoal)

	sub := s.cfg.SubAgents[role]
	if sub == nil {
		s.lastObs = Wrap("TOOL_FAILED",
			fmt.Sprintf("handoff to unknown role %q failed; handle the subtask yourself or finish", role))
		return edgeOK, nil
	}

	task, _ := s.store.Get(s.taskID)
	s.emit(ActHandoffIn, role+": "+subgoal, "")

	// Cap the sub-agent run so a wedged specialist can't pin the parent loop
	// forever (autogen's RPC-timeout pattern). The parent ctx still cancels it
	// early; this only adds an upper bound. A timeout is surfaced as a normal
	// observation, never a graph error, so the parent keeps control.
	subCtx, cancel := context.WithTimeout(ctx, s.cfg.SubAgentTimeout)
	defer cancel()
	res, err := sub.RunSubtask(subCtx, task.SessionID, subgoal, s.taskID)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err() // PARENT cancelled — propagate
		}
		reason := err.Error()
		if subCtx.Err() == context.DeadlineExceeded {
			reason = fmt.Sprintf("timed out after %s", s.cfg.SubAgentTimeout)
		}
		s.lastObs = Wrap("TOOL_FAILED",
			fmt.Sprintf("sub-agent %q failed on subtask %q: %s; handle it yourself or finish", role, subgoal, reason))
		s.emit(ActHandoffOut, role+" → failed: "+reason, "")
		return edgeOK, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Sub-agent %q completed subtask: %s\n", role, subgoal)
	if res.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", res.Summary)
	}
	if len(res.Findings) > 0 {
		b.WriteString("Findings (also recorded to shared knowledge):\n")
		for _, f := range res.Findings {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	s.lastObs = Wrap("TOOL_RESPONSE", b.String())
	s.emit(ActHandoffOut, role+" → "+orDefault(res.Summary, "completed"), "")
	return edgeOK, nil
}
