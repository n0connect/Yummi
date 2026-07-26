package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// stepNode asks the LLM for the next control action and routes on its tag.
// Token pressure and budget are checked before the LLM call.
func stepNode(ctx context.Context, s *GraphState) (string, error) {
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}
	// Hard halt: if the model could not recover after repeated forceful
	// REPEATED_FAILURE directives (2× the limit of back-to-back failures),
	// stop burning budget and hand control to the operator.
	if s.consecutiveFailures >= 2*s.failureLimit() {
		s.lastErr = "repeated command failures exhausted autonomous recovery"
		s.pendingAct = Action{Question: fmt.Sprintf(
			"Halted after %d consecutive command failures with no recovery. Operator guidance needed.",
			s.consecutiveFailures)}
		return edgeAskOperator, nil
	}
	// Token pressure check before building the prompt.
	if s.memCtx.NeedsCompression() {
		return edgeContextFull, nil
	}
	// Budget exhaustion is a FAILURE: the goal was not reached in time.
	if task.StepsUsed >= task.StepBudget {
		s.finishOutcome = outcomeFailed
		s.finishMsg = fmt.Sprintf("Step budget (%d) exhausted before the goal was achieved. Last observation:\n%s",
			task.StepBudget, CompressObservation(s.lastObs))
		return edgeBudgetOut, nil
	}

	system := StepSystemPrompt(s.cfg.Persona)
	prompt := BuildStepPrompt(task, s.lastObs, s.memCtx.Summary, s.buildPreamble(ctx, task))

	raw, err := s.llm.Chat(ctx, system, prompt)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		slog.Warn("agent: step LLM error", "task", s.taskID, "error", err)
		s.retryCtx = RetryContext{Caller: nodeStep, Attempts: s.retryCtx.Attempts}
		return edgeLLMError, nil
	}

	msg, parseErr := ParseControl(raw)
	if parseErr != nil {
		// Single inline parse-retry with a format hint before escalating to RetryNode.
		hint := llmParseRetryHint(s.retryCtx.Attempts)
		raw2, err2 := s.llm.Chat(ctx, system, prompt+hint)
		if err2 != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			s.retryCtx = RetryContext{Caller: nodeStep, Attempts: s.retryCtx.Attempts}
			return edgeLLMError, nil
		}
		msg, parseErr = ParseControl(raw2)
		if parseErr != nil {
			s.retryCtx = RetryContext{Caller: nodeStep, Attempts: s.retryCtx.Attempts}
			return edgeLLMError, nil
		}
	}

	s.retryCtx = RetryContext{} // reset on successful parse

	// SEND_FULL and STOP_TASK never consume the step budget: SEND_FULL is a
	// context request; STOP_TASK terminates the task.
	switch msg.Tag {
	case "SEND_FULL":
		s.lastObs = Wrap("TOOL_RESPONSE_FULL", lastFullObservation(s))
		return edgeSendFull, nil
	case "STOP_TASK":
		s.finishOutcome = outcomeStopped
		s.finishMsg = orDefault(msg.Action.Reason, "stopped by model")
		return edgeStopTask, nil
	}

	// All remaining control actions consume one step budget unit.
	_ = s.store.Update(s.taskID, func(t *Task) { t.StepsUsed++ })
	s.pendingAct = msg.Action

	switch msg.Tag {
	case "RUN_COMMAND":
		if strings.TrimSpace(msg.Action.Command) == "" {
			return s.rejectStepAction()
		}
		return edgeRunCommand, nil
	case "FINISH":
		s.finishOutcome = outcomeCompleted
		s.finishMsg = msg.Action.Summary
		if s.finishMsg == "" {
			s.finishMsg = "Task finished."
		}
		return edgeFinish, nil
	case "HANDOFF":
		// Delegation is only available when sub-agents are configured (the
		// orchestrator). A sub-agent has an empty SubAgents map, so a HANDOFF it
		// emits — or one naming an unknown role / empty subgoal — is rejected as
		// an invalid action rather than wedging the loop. This caps depth at 1.
		// handoffTargets normalizes the single-target (Role/Subgoal) and
		// fan-out (Handoffs) forms into one list; every entry must resolve to a
		// configured sub-agent, or the whole action is rejected — a partially
		// valid fan-out silently dropping a target would be a confusing way to
		// fail.
		targets := handoffTargets(msg.Action)
		if len(targets) == 0 {
			return s.rejectStepAction()
		}
		for _, t := range targets {
			if _, ok := s.cfg.SubAgents[strings.TrimSpace(t.Role)]; !ok || strings.TrimSpace(t.Subgoal) == "" {
				return s.rejectStepAction()
			}
		}
		return edgeHandoff, nil
	case "REMEMBER":
		if strings.TrimSpace(msg.Action.MemKey) == "" || strings.TrimSpace(msg.Action.MemValue) == "" {
			return s.rejectStepAction()
		}
		return edgeRemember, nil
	case "ASK_OPERATOR":
		if strings.TrimSpace(msg.Action.Question) == "" {
			return s.rejectStepAction()
		}
		return edgeAskOperator, nil
	case "UPDATE_PLAN":
		if len(msg.Action.Steps) == 0 {
			return s.rejectStepAction()
		}
		_ = s.store.Update(s.taskID, func(t *Task) { t.Plan = msg.Action.Steps })
		s.lastObs = Wrap("PLAN_UPDATED", msg.Action.Reason)
		return edgeUpdatePlan, nil
	default:
		// CONTINUE/ABORT/CHANGE_GOAL are consult-only and invalid at a normal step.
		return s.rejectStepAction()
	}
}

// rejectStepAction is the shared outcome for a structurally-parsed control
// message whose required field(s) are missing/invalid at the step node: route
// to the LLM-error retry edge with a fresh retry context (Attempts starts at
// 0 — this is a new validation failure, not a continuation of an LLM-call
// failure, so it must NOT reuse whatever Attempts count node_step's own
// LLM-call-error paths above carry).
func (s *GraphState) rejectStepAction() (string, error) {
	s.retryCtx = RetryContext{Caller: nodeStep}
	return edgeLLMError, nil
}

// buildPreamble assembles the per-turn context blocks injected ahead of the
// task state: SKILLS (how to approach this class of target), DELEGATION (which
// specialists are available), and MEMORY (what we already learned). Each block
// is independent and may be empty; they are joined with blank lines.
func (s *GraphState) buildPreamble(ctx context.Context, task Task) string {
	var parts []string
	if sk := s.buildSkills(task); sk != "" {
		parts = append(parts, sk)
	}
	if dl := s.buildDelegation(); dl != "" {
		parts = append(parts, dl)
	}
	if mem := s.buildMemory(ctx, task); mem != "" {
		parts = append(parts, mem)
	}
	return strings.Join(parts, "\n")
}

// buildSkills selects and renders the playbooks relevant to the goal and the
// tools actually used so far. Empty when no registry / no match.
func (s *GraphState) buildSkills(task Task) string {
	if s.cfg.Skills == nil {
		return ""
	}
	sel := s.cfg.Skills.Select(task.Goal, toolsFromCommands(s.recentCmds), 3)
	if len(sel) > 0 {
		names := make([]string, len(sel))
		for i, sk := range sel {
			names[i] = sk.Name
		}
		if sig := strings.Join(names, ", "); sig != s.lastSkillsSig {
			s.lastSkillsSig = sig
			s.emit(ActSkill, sig, "")
		}
	}
	return s.cfg.Skills.Render(sel)
}

// buildDelegation lists the sub-agent roles the orchestrator may hand off to.
// Empty for sub-agents (no SubAgents configured), so they never see the option.
func (s *GraphState) buildDelegation() string {
	if len(s.cfg.SubAgents) == 0 {
		return ""
	}
	roles := make([]string, 0, len(s.cfg.SubAgents))
	for r := range s.cfg.SubAgents {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return "DELEGATION: hand a focused subtask to a specialist with " +
		`[HANDOFF] {"role":"<role>","subgoal":"<one clear sentence>"}` +
		". To delegate to SEVERAL specialists AT ONCE — they run concurrently, not one after another — use " +
		`[HANDOFF] {"handoffs":[{"role":"<role>","subgoal":"<...>"},{"role":"<role>","subgoal":"<...>"}]}` +
		". Available roles: " + strings.Join(roles, ", ") + ".\n"
}

// buildMemory queries the retrieval strategy for facts/tactics relevant to the
// current task and renders a compact MEMORY block (empty string when nothing,
// or when retrieval is not configured).
func (s *GraphState) buildMemory(ctx context.Context, task Task) string {
	if s.cfg.Retrieval == nil {
		return ""
	}
	items, err := s.cfg.Retrieval.Retrieve(ctx, knowledge.Query{
		SessionID: task.SessionID, Tools: toolsFromCommands(s.recentCmds), Limit: 5,
		// Suppress recall of commands already run this task: re-injecting them as
		// "use it, don't rediscover" reinforces weak models into command loops.
		ExcludeCommands: s.recentCmds,
	})
	if err != nil || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MEMORY (prior knowledge — use it, don't rediscover):\n")
	for _, it := range items {
		fmt.Fprintf(&b, "  • %s\n", it.Text)
	}
	if sig := items[0].Text; sig != s.lastMemorySig {
		s.lastMemorySig = sig
		s.emit(ActMemory, items[0].Text, "")
	}
	return b.String()
}

// toolsFromCommands returns the distinct tool names (first token) of the most
// recently EXECUTED commands. This is keyed the same way the executor records
// tactics (also the command's first token), so procedural retrieval lines up
// with what was actually learned — unlike plan-step descriptions, which are
// natural-language and rarely start with a real tool name.
func toolsFromCommands(cmds []string) []string {
	seen := map[string]bool{}
	var tools []string
	for _, c := range cmds {
		f := strings.Fields(c)
		if len(f) == 0 || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		tools = append(tools, f[0])
	}
	return tools
}
