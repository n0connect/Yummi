package eino

import (
	"context"
	"fmt"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// brief.go builds the per-task TASK BRIEF: the goal, the MEMORY notes from
// earlier turns, the SKILLS index, the most relevant PLAYBOOK, and the goal's
// parts checklist. The brief is the first thing the model reads on every Run.
//
// Why it lives in the opening message and not the static Instruction: each
// Run() starts from a fresh user message and carries no history forward, so the
// model only sees what that message contains. A block buried after a long
// system prompt was reproducibly ignored. The live run that exposed this
// answered "no notes found" while its own note was in the prompt. Putting the
// brief in every opening message (the first Run, every nudge, every retry) makes
// the context deterministic, not dependent on how the model weights a long
// system prompt.

// playbookCharCap bounds each pushed playbook (~1K tokens). A playbook longer
// than this is cut, and the cut says where the rest lives (skill_view).
const playbookCharCap = 4000

// maxPushedPlaybooks bounds how many goal-matched playbooks the system pushes.
// Measured live (2026-10-09): with one push the model used the matched playbook
// for its first target but never loaded a second, equally-matched one (SNMP
// work was done by raw commands although its playbook existed). Pushing every
// strong match deterministically removes that dependence on the model
// choosing to call skill_view.
const maxPushedPlaybooks = 3

// buildPlaybookBlock pushes the goal's strongest playbook matches (up to
// maxPushedPlaybooks, best first) once per task. Returns the block and the
// pushed names, in order. Empty when no playbook matches.
func buildPlaybookBlock(skills *skill.Registry, goal string) (block string, names []string) {
	sel := skills.Select(goal, nil, maxPushedPlaybooks)
	if len(sel) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("PLAYBOOKS for this goal (their method; follow them unless the operator says otherwise):\n")
	for _, top := range sel {
		body := skill.RenderBody(top)
		if runes := []rune(body); len(runes) > playbookCharCap {
			body = string(runes[:playbookCharCap]) + "\n…(playbook truncated; load the rest with skill_view)\n"
		}
		b.WriteString(body)
		names = append(names, top.Name)
	}
	return b.String(), names
}

// composeTaskBrief joins the brief's sections in a fixed order. Empty sections
// are omitted. The goal always leads, so the model knows the objective before it
// reads the context around it.
func composeTaskBrief(goal string, parts []string, memory, skillIndex, playbook string) string {
	var b strings.Builder
	b.WriteString("TASK BRIEF — read it before your first action.\n")
	fmt.Fprintf(&b, "Goal: %s\n", strings.TrimSpace(goal))
	if len(parts) >= 2 {
		b.WriteString("\nThis goal has multiple distinct parts. Address EACH one — do not call finish until every part below has been genuinely attempted, not just the first one or two:\n")
		for i, p := range parts {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(p))
		}
	}
	if memory != "" {
		b.WriteString("\n" + strings.TrimRight(memory, "\n") + "\n")
	}
	if skillIndex != "" {
		b.WriteString("\n" + strings.TrimRight(skillIndex, "\n") + "\n")
	}
	if playbook != "" {
		b.WriteString("\n" + strings.TrimRight(playbook, "\n") + "\n")
	}
	return b.String()
}

// prefixBrief puts the brief in front of one opening message. An empty brief
// returns the message unchanged. Every Run's opening message goes through this,
// so a retry or nudge carries the same context as the first attempt.
func prefixBrief(brief, msg string) string {
	if brief == "" {
		return msg
	}
	return brief + "\n\n" + msg
}

// taskBrief assembles the brief for one task and records the activities it
// implies, so TRACE shows what the model was given before its first action:
// ActMemory when notes were applied, ActSkillSuggested when the system pushed
// a playbook (NOT the model's own choice — see ActSkillSuggested's doc
// comment), and ActPlan when the goal was split into parts.
func (r *EinoRunner) taskBrief(ctx context.Context, sessionID, taskID, goal string, parts []string) string {
	memory := buildMemoryBlock(ctx, r.cfg.Retrieval, sessionID, goal)
	skillIndex := buildSkillsBlock(r.cfg.Skills, goal)
	playbook, playbookNames := buildPlaybookBlock(r.cfg.Skills, goal)

	if memory != "" {
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: sessionID, TaskID: taskID, Kind: agent.ActMemory, Summary: "Relevant session context applied",
		})
	}
	for _, name := range playbookNames {
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: sessionID, TaskID: taskID, Kind: agent.ActSkillSuggested, Summary: name,
		})
	}
	if len(parts) >= 2 {
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: sessionID, TaskID: taskID, Kind: agent.ActPlan,
			Summary: fmt.Sprintf("%d goal parts detected — tracking each in the task brief", len(parts)),
		})
	}
	return composeTaskBrief(goal, parts, memory, skillIndex, playbook)
}

// resumeNoteBrief states, in the brief, that this run is a continuation and
// what the operator answered. It lives in the brief, not only the first
// message, so the continuation context survives nudges and retries too.
func resumeNoteBrief(priorQuestion, answer string) string {
	if priorQuestion == "" {
		return "RESUMED: this run continues an earlier one that paused. Operator's guidance: " + answer
	}
	return fmt.Sprintf("RESUMED: this run continues an earlier one that paused and asked the operator: %q. Operator's answer: %s", priorQuestion, answer)
}
