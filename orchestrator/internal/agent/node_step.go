package agent

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
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
	// Give the model one final chance to provide a structured FINISH (and its
	// evidence-bearing finding details). If it emits anything else, fails, or
	// formats the control message poorly, the terminal plan is synthesized
	// deterministically below instead of drifting into more commands.
	terminalPlan := s.autoFinishTerminalPlan && task.StepsUsed > 0 && planTerminal(task.Plan) && planHasSuccess(task.Plan) && hasSynthesisEvidence(s.memCtx)

	// Deliver any operator chat message sent WHILE this task was already
	// running (see Task.PendingGuidance's doc comment / Store.DrainGuidance).
	// stepNode runs on every single turn, so draining here — rather than at
	// toolNode's many separate exit points — guarantees delivery exactly
	// once, on the model's very next turn, regardless of which edge the
	// previous node took. s.lastObs itself is left untouched (it feeds the
	// budget-exhaustion message below and other callers); the guidance is
	// folded only into this turn's rendered prompt.
	stepObs := s.lastObs
	if s.store != nil {
		if notes := s.store.DrainGuidance(s.taskID); len(notes) > 0 {
			guidance := Wrap("OPERATOR_GUIDANCE", "The operator sent this WHILE you were working. "+
				"Read it and adjust your next action accordingly:\n"+strings.Join(notes, "\n"))
			if stepObs == "" {
				stepObs = guidance
			} else {
				stepObs = stepObs + "\n\n" + guidance
			}
		}
	}

	system := StepSystemPrompt(s.cfg.Persona, s.cfg.MCPCatalog, s.cfg.LocalModel)
	prompt := BuildStepPrompt(task, stepObs, s.memCtx.Summary, s.buildPreamble(ctx, task))

	// NeedsCompression's check above only tracks raw observation bytes added
	// via AddObservation — it has no visibility into the persona, plan
	// rendering, or skills/delegation/memory preamble that also ride in
	// every prompt, nor into the compressed Summary's own weight after a
	// prior compression pass reset TokensUsed to 0. Recording the actual
	// prompt size we just built (never shrinking it — a smaller reading here
	// would just be this turn's Observations being momentarily lower, not
	// real pressure easing) keeps next turn's compression decision anchored
	// to what is really being sent, not just the observation-byte proxy.
	if real := EstimateTokens(system) + EstimateTokens(prompt); real > s.memCtx.TokensUsed {
		s.memCtx.TokensUsed = real
	}

	raw, err := s.llm.Chat(ctx, system, prompt)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if terminalPlan {
			return s.convergeTerminalPlan()
		}
		slog.Warn("agent: step LLM error", "task", s.taskID, "error", err)
		// A context-window overflow is not transient: re-sending the identical
		// prompt can never succeed. Force the compaction route instead, bounded
		// so an overflow compaction cannot cure falls through to the normal
		// retry/give-up path rather than looping.
		if IsContextOverflow(err) && len(s.memCtx.Observations) > 0 && s.overflowCompactions < maxOverflowCompactions {
			s.overflowCompactions++
			slog.Warn("agent: context window overflow; forcing compaction", "task", s.taskID, "attempt", s.overflowCompactions)
			s.memCtx.TokensUsed = s.memCtx.TokenBudget
			s.retryCtx = RetryContext{Caller: nodeStep}
			return edgeContextFull, nil
		}
		if IsContextOverflow(err) {
			// Compaction is exhausted (or had nothing to compact): say so plainly
			// instead of letting the generic "LLM unavailable" message stand.
			s.lastErr = fmt.Sprintf("context window exhausted after %d compaction(s); the goal needs a narrower scope or a new task", s.overflowCompactions)
		}
		s.retryCtx = RetryContext{Caller: nodeStep, Attempts: s.retryCtx.Attempts}
		return edgeLLMError, nil
	}

	msg, parseErr := ParseControl(raw)
	if parseErr != nil {
		if terminalPlan {
			return s.convergeTerminalPlan()
		}
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
	if terminalPlan && msg.Tag != "FINISH" && msg.Tag != "STOP_TASK" {
		return s.convergeTerminalPlan()
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
	case "MCP_TOOL":
		// Routes to the SAME tool node RUN_COMMAND uses — toolNode itself
		// branches on action.Tool vs action.Command (see node_tool.go). No new
		// edge needed; this is deliberately the smallest possible change to
		// "the deliberately-designed graph flow" (see graph.go's own warning).
		if strings.TrimSpace(msg.Action.Tool) == "" {
			return s.rejectStepAction()
		}
		return edgeRunCommand, nil
	case "FINISH":
		// Premature-FINISH guard: a multi-part goal that decomposed into
		// several plan steps must not be closed while any of those steps is
		// still pending/running. The model — regardless of size — will often
		// answer the first part of a goal and then emit FINISH, and nothing
		// downstream re-opens the loop. Bounce the first such FINISH back with
		// an explicit list of the goals it has not addressed yet; if it insists
		// on FINISH after seeing that list, obey (the remaining work may be
		// genuinely unreachable). One forced reconsideration, then trust.
		if s.autoFinishTerminalPlan && s.incompleteFinishNudges < maxIncompleteFinishNudges {
			if pending := pendingStepDescriptions(task.Plan); len(pending) > 0 &&
				task.StepBudget-task.StepsUsed >= 2 {
				s.incompleteFinishNudges++
				s.lastObs = Wrap("INCOMPLETE_GOALS", fmt.Sprintf(
					"You attempted to FINISH, but the goal has %d part(s) you have NOT addressed yet:\n%s\n"+
						"Each is a distinct part of the GOAL. Run a command to complete the next one. "+
						"Only FINISH once every part is done, or mark a part unreachable with UPDATE_PLAN first.",
					len(pending), strings.Join(pending, "\n")))
				return edgeUpdatePlan, nil
			}
		}
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
		if len(targets) > s.cfg.MaxHandoffsPerStep {
			s.lastObs = Wrap("TOOL_FAILED", fmt.Sprintf(
				"HANDOFF requested %d specialists; the per-step safety limit is %d. Split the work or choose the most relevant roles.",
				len(targets), s.cfg.MaxHandoffsPerStep))
			return edgeUpdatePlan, nil
		}
		for _, t := range targets {
			if _, ok := s.cfg.SubAgents[strings.TrimSpace(t.Role)]; !ok || strings.TrimSpace(t.Subgoal) == "" {
				return s.rejectStepAction()
			}
		}
		return edgeHandoff, nil
	case "REMEMBER":
		// An empty value is valid: it forgets the key (see ApplyNote).
		if strings.TrimSpace(msg.Action.MemKey) == "" {
			return s.rejectStepAction()
		}
		return edgeRemember, nil
	case "SKILL_VIEW":
		if strings.TrimSpace(msg.Action.SkillName) == "" {
			return s.rejectStepAction()
		}
		return edgeSkillView, nil
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

func (s *GraphState) convergeTerminalPlan() (string, error) {
	s.pendingAct = Action{}
	s.finishOutcome = outcomeCompleted
	s.finishMsg = ""
	return edgeFinish, nil
}

func hasSynthesisEvidence(memory MemoryContext) bool {
	if strings.TrimSpace(memory.Summary) != "" {
		return true
	}
	for _, observation := range memory.Observations {
		if strings.TrimSpace(observation) != "" {
			return true
		}
	}
	return false
}

// maxIncompleteFinishNudges bounds how many times stepNode bounces a premature
// FINISH back to the model before obeying it — one, so a genuinely-unreachable
// remaining goal never wedges the loop.
const maxIncompleteFinishNudges = 1

// pendingStepDescriptions returns the human-readable descriptions of plan steps
// still pending or running — the goals a FINISH would leave unaddressed. Used
// by the premature-FINISH guard to tell the model exactly what remains.
func pendingStepDescriptions(plan []PlanStep) []string {
	var out []string
	for _, step := range plan {
		if step.Status == StepPending || step.Status == StepRunning {
			desc := strings.TrimSpace(step.Description)
			if desc == "" {
				desc = strings.TrimSpace(step.ID)
			}
			out = append(out, "  - "+desc)
		}
	}
	return out
}

func planTerminal(plan []PlanStep) bool {
	if len(plan) == 0 {
		return false
	}
	for _, step := range plan {
		if step.Status == StepPending || step.Status == StepRunning {
			return false
		}
	}
	return true
}

func planHasSuccess(plan []PlanStep) bool {
	for _, step := range plan {
		if step.Status == StepDone {
			return true
		}
	}
	return false
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
	if s.cfg.LocalModel {
		if tr := targetReminder(task.Goal); tr != "" {
			parts = append(parts, tr)
		}
	}
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

// goalIPv4Re is a lenient free-text scan for an IPv4 address — unlike
// internal/scope.ExtractTarget (built for a shell command's argv shape,
// where token 0 is always skipped as the binary name), a goal is prose, and
// the address can carry trailing sentence punctuation ExtractTarget was
// never designed to strip ("...against 198.51.100.10. The target..."). Not
// validated beyond the shape (no 0-255 octet range check) — this is a
// prompt-prominence aid, not a security boundary; the gateway is the real
// scope authority regardless of what this finds.
var goalIPv4Re = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// targetReminder restates the goal's target IP as its own short, unmissable
// line — local models only. Live-reproduced why this exists: given the goal
// "Start a full autonomous pentest task against 198.51.100.10. ... 1) ...
// 2) ... 3) ... 4) ...", jinx-20b targeted 127.0.0.1 four turns in a row
// instead (gateway correctly blocked every attempt — zero scope leakage,
// this was never a security issue, only a wasted task). The IP was present
// in GOAL: at the very top of the prompt the whole time; burying a single
// numeric fact inside a long prose sentence, mixed with four enumerated
// sub-goals, evidently wasn't enough for a small model to reliably carry
// forward into its next tool call. Empty when the goal has no clear
// single IPv4 address (hostnames, multi-target goals, or no target at all)
// — never a guess, only a restatement of what's already unambiguously there.
func targetReminder(goal string) string {
	ip := goalIPv4Re.FindString(goal)
	if ip == "" {
		return ""
	}
	return fmt.Sprintf("TARGET: %s — every command in this task must target exactly this address. Do NOT use 127.0.0.1, localhost, or any other address.", ip)
}

// buildSkills renders the SKILLS index: the catalogue of playbooks, with the
// ones matching this goal suggested first. It is the same on every step, for
// both engines, and it is small and bounded (skill.DefaultIndexBudget), so a
// local model always knows which playbooks exist. A playbook's full text is
// NOT injected here. The model loads it with [SKILL_VIEW] when a step reaches
// that technique (see skillViewNode), so no step pays for playbooks it does not
// use. Empty when there is no registry or no skills.
func (s *GraphState) buildSkills(task Task) string {
	return s.cfg.Skills.Index(task.Goal, toolsFromCommands(s.recentCmds), skill.DefaultIndexBudget)
}

// buildDelegation lists the sub-agent roles the orchestrator may hand off to.
// Empty for sub-agents (no SubAgents configured), so they never see the option.
func (s *GraphState) buildDelegation() string {
	if len(s.cfg.SubAgents) == 0 {
		return ""
	}
	return "DELEGATION: hand a focused subtask to a specialist with " +
		`[HANDOFF] {"role":"<role>","subgoal":"<one clear sentence>"}` +
		". To delegate to SEVERAL specialists AT ONCE — they run concurrently, not one after another — use " +
		`[HANDOFF] {"handoffs":[{"role":"<role>","subgoal":"<...>"},{"role":"<role>","subgoal":"<...>"}]}` +
		". " + FormatDelegationRoles(s.cfg.SubAgents, s.cfg.SubAgentHints)
}

// buildMemory queries the retrieval strategy for facts/tactics relevant to the
// current task and renders a compact MEMORY block (empty string when nothing,
// or when retrieval is not configured).
func (s *GraphState) buildMemory(ctx context.Context, task Task) string {
	if s.cfg.Retrieval == nil {
		return ""
	}
	items, err := s.cfg.Retrieval.Retrieve(ctx, knowledge.Query{
		SessionID: task.SessionID, Goal: task.Goal, Tools: toolsFromCommands(s.recentCmds), Limit: 5,
		// Suppress recall of commands already run this task: re-injecting them as
		// "use it, don't rediscover" reinforces weak models into command loops.
		ExcludeCommands: s.recentCmds,
	})
	if err != nil || len(items) == 0 {
		return ""
	}
	if sig := items[0].Text; sig != s.lastMemorySig {
		s.lastMemorySig = sig
		s.emit(ActMemory, "Relevant session context applied", "")
	}
	return FormatMemoryItems(items)
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

// maxOverflowCompactions bounds forced compactions per task (see GraphState's
// overflowCompactions).
const maxOverflowCompactions = 3
