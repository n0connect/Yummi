package eino

import (
	"context"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// buildMemoryBlock queries the retrieval strategy for facts/tactics relevant
// to the goal and renders a MEMORY block via agent.FormatMemoryItems — the
// same rendering node_step.go's buildMemory uses for the graph backend.
// Unlike graph — which rebuilds the whole prompt from scratch every step and
// so re-queries every turn — Eino's ChatModelAgent keeps native chat history
// across its own tool-calling turns, so this is deliberately queried once, up
// front, and folded into the static Instruction: that's the correct one-time
// analogue of graph's per-step re-injection for a backend that already
// retains its own history. Empty string when retrieval is nil or nothing
// comes back.
func buildMemoryBlock(ctx context.Context, retrieval knowledge.RetrievalStrategy, sessionID, goal string) string {
	if retrieval == nil {
		return ""
	}
	items, err := retrieval.Retrieve(ctx, knowledge.Query{SessionID: sessionID, Goal: goal, Limit: 5})
	if err != nil {
		return ""
	}
	return agent.FormatMemoryItems(items)
}

// buildSkillsBlock renders the SKILLS index, the same catalogue node_step.go's
// buildSkills renders per step on the graph engine: every playbook by name,
// goal matches suggested first. Full playbook bodies are not folded in here;
// the model loads one with the skill_view tool when a step reaches that
// technique (see skill_tool.go). Empty when there is no registry or no skills.
// *skill.Registry methods tolerate a nil receiver, matching the graph backend.
func buildSkillsBlock(skills *skill.Registry, goal string) string {
	return skills.Index(goal, nil, skill.DefaultIndexBudget)
}

// buildDelegationBlock lists the sub-agent roles the handoff tool can reach,
// via agent.FormatDelegationRoles — the same role-list rendering
// node_step.go's buildDelegation uses for the graph backend (the two differ
// only in which delegation mechanism they instruct the model to use). Empty
// when no sub-agents are configured (delegation simply doesn't show up as an
// option). hints maps a role name to its short routing-hint sentence
// (agent.Config.SubAgentHints); a role with no hint (or a nil map) still
// renders as a bare name.
func buildDelegationBlock(subAgents map[string]agent.SubAgentRunner, hints map[string]string) string {
	if len(subAgents) == 0 {
		return ""
	}
	return "DELEGATION: call the handoff tool to hand a focused subtask to a specialist. Call handoff more " +
		"than once in a turn to delegate to several specialists at once — they run concurrently, not one after " +
		"another. " + agent.FormatDelegationRoles(subAgents, hints)
}
