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
	res, err := sub.RunSubtask(ctx, task.SessionID, subgoal)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.lastObs = Wrap("TOOL_FAILED",
			fmt.Sprintf("sub-agent %q failed on subtask %q: %v", role, subgoal, err))
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
	return edgeOK, nil
}
