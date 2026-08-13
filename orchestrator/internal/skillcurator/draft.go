package skillcurator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// Trajectory is the successful task history a candidate skill is drafted
// from. Carries no target/session-identifying content beyond IDs — the
// tactics it references are already target-scrubbed by knowledge.Store.
type Trajectory struct {
	TaskID       string
	SessionID    string
	Goal         string
	Summary      string
	Observations []string
	Tactics      []knowledge.Tactic
}

// empty reports whether the trajectory has nothing worth drafting a skill
// from — DraftSkill rejects this before ever calling the LLM.
func (t Trajectory) empty() bool {
	return strings.TrimSpace(t.Goal) == "" && strings.TrimSpace(t.Summary) == "" &&
		len(t.Tactics) == 0 && len(t.Observations) == 0
}

// ChatFunc matches llm.ChatFunc's shape structurally (system+user -> reply)
// so the curator can be wired to llm.Resilient.Chat or a plain *llm.Client
// without skillcurator importing llm as a concrete dependency.
type ChatFunc func(ctx context.Context, system, user string) (string, error)

const draftSystemPrompt = `You convert a completed pentest task's trajectory into a reusable skill definition.
Respond with ONLY a JSON object, no prose, matching exactly:
{"name":"kebab-case-name","description":"one sentence","triggers":["keyword1","keyword2"],"tools":["tool1"],"knowledge":"a short, actionable playbook description of the approach"}`

// DraftSkill prompts chat to turn a trajectory into a skill.Skill. Returns an
// error (without calling chat) for an empty trajectory — there is nothing to
// generalize from.
func DraftSkill(ctx context.Context, chat ChatFunc, traj Trajectory) (skill.Skill, error) {
	traj = SanitizeTrajectory(traj)
	if traj.empty() {
		return skill.Skill{}, fmt.Errorf("draft skill: empty trajectory, nothing to learn from")
	}

	var b strings.Builder
	if traj.Goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", traj.Goal)
	}
	if traj.Summary != "" {
		fmt.Fprintf(&b, "Outcome: %s\n", traj.Summary)
	}
	if len(traj.Tactics) > 0 {
		b.WriteString("Tactics used:\n")
		for _, tac := range traj.Tactics {
			fmt.Fprintf(&b, "- %s (%s, %s)\n", tac.CommandPattern, tac.Tool, tac.Outcome)
		}
	}
	if len(traj.Observations) > 0 {
		b.WriteString("Observations:\n")
		for _, obs := range traj.Observations {
			fmt.Fprintf(&b, "- %s\n", obs)
		}
	}

	reply, err := chat(ctx, draftSystemPrompt, b.String())
	if err != nil {
		return skill.Skill{}, fmt.Errorf("draft skill: chat: %w", err)
	}

	var s skill.Skill
	if err := json.Unmarshal([]byte(stripCodeFence(reply)), &s); err != nil {
		return skill.Skill{}, fmt.Errorf("draft skill: parse LLM response: %w", err)
	}
	if strings.TrimSpace(s.Name) == "" {
		return skill.Skill{}, fmt.Errorf("draft skill: LLM response missing a name")
	}
	s.Provenance = skill.Provenance{Origin: "auto", Source: "task:" + traj.TaskID}
	s = skill.Normalize(s)
	if err := skill.ValidateSchema(s); err != nil {
		return skill.Skill{}, fmt.Errorf("draft skill: validation: %w", err)
	}
	return s, nil
}

// stripCodeFence removes a leading/trailing ```json ... ``` (or bare ```)
// wrapper some models add around JSON output, despite being told not to.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
