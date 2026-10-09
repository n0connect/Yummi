package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// skillViewNode answers a [SKILL_VIEW] action with the full text of one
// playbook. The SKILLS index in the prompt names what exists, and this node is
// how the model gets a playbook's method into its context, on demand. Loading
// it is recorded as an ActSkill activity, so the TRACE view shows which
// playbooks a task actually used, not just which ones were on offer.
//
// A nil or empty registry, or an unknown name, is a recoverable TOOL_FAILED
// observation that points back at the index. It never fails the task.
func skillViewNode(_ context.Context, s *GraphState) (string, error) {
	name := strings.TrimSpace(s.pendingAct.SkillName)
	sk, ok := s.cfg.Skills.Lookup(name)
	if !ok {
		s.lastObs = Wrap("TOOL_FAILED", fmt.Sprintf(
			"no playbook named %q — use an exact name from the SKILLS index", name))
		return edgeOK, nil
	}
	s.emit(ActSkill, sk.Name, "")
	s.lastObs = Wrap("TOOL_RESPONSE", skill.RenderBody(sk))
	return edgeOK, nil
}
