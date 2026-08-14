package eino

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// buildMemoryBlock queries the retrieval strategy for facts/tactics relevant
// to the goal and renders a MEMORY block, in the same format node_step.go's
// buildMemory uses for the graph backend. Unlike graph — which rebuilds the
// whole prompt from scratch every step and so re-queries every turn — Eino's
// ChatModelAgent keeps native chat history across its own tool-calling turns,
// so this is deliberately queried once, up front, and folded into the
// static Instruction: that's the correct one-time analogue of graph's
// per-step re-injection for a backend that already retains its own history.
// Empty string when retrieval is nil or nothing comes back.
func buildMemoryBlock(ctx context.Context, retrieval knowledge.RetrievalStrategy, sessionID, goal string) string {
	if retrieval == nil {
		return ""
	}
	items, err := retrieval.Retrieve(ctx, knowledge.Query{SessionID: sessionID, Goal: goal, Limit: 5})
	if err != nil || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MEMORY (prior knowledge — use it, don't rediscover):\n")
	for _, it := range items {
		fmt.Fprintf(&b, "  • %s\n", it.Text)
	}
	return b.String()
}

// buildSkillsBlock selects and renders the playbooks relevant to the goal,
// mirroring node_step.go's buildSkills — queried once up front for the same
// reason buildMemoryBlock is. Empty when no registry / no match. *skill.Registry
// methods tolerate a nil receiver, matching the graph backend's own usage.
// names is the selected skills' names, for the caller's activity emit.
func buildSkillsBlock(skills *skill.Registry, goal string) (block string, names []string) {
	sel := skills.Select(goal, nil, 3)
	for _, sk := range sel {
		names = append(names, sk.Name)
	}
	return skills.Render(sel), names
}

// buildDelegationBlock lists the sub-agent roles the handoff tool can reach,
// mirroring node_step.go's buildDelegation. Empty when no sub-agents are
// configured (delegation simply doesn't show up as an option).
func buildDelegationBlock(subAgents map[string]*agent.Runner) string {
	if len(subAgents) == 0 {
		return ""
	}
	roles := make([]string, 0, len(subAgents))
	for r := range subAgents {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return "DELEGATION: call the handoff tool to hand a focused subtask to a specialist. Available roles: " +
		strings.Join(roles, ", ") + ". Call handoff more than once in a turn to delegate to several specialists at once " +
		"— they run concurrently, not one after another.\n"
}
