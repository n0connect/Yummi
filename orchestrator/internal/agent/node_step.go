package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	prompt := BuildStepPrompt(task, s.lastObs, s.memCtx.Summary)

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
			s.retryCtx = RetryContext{Caller: nodeStep}
			return edgeLLMError, nil
		}
		return edgeRunCommand, nil
	case "FINISH":
		s.finishOutcome = outcomeCompleted
		s.finishMsg = msg.Action.Summary
		if s.finishMsg == "" {
			s.finishMsg = "Task finished."
		}
		return edgeFinish, nil
	case "ASK_OPERATOR":
		if strings.TrimSpace(msg.Action.Question) == "" {
			s.retryCtx = RetryContext{Caller: nodeStep}
			return edgeLLMError, nil
		}
		return edgeAskOperator, nil
	case "UPDATE_PLAN":
		if len(msg.Action.Steps) == 0 {
			s.retryCtx = RetryContext{Caller: nodeStep}
			return edgeLLMError, nil
		}
		_ = s.store.Update(s.taskID, func(t *Task) { t.Plan = msg.Action.Steps })
		s.lastObs = Wrap("PLAN_UPDATED", msg.Action.Reason)
		return edgeUpdatePlan, nil
	default:
		// CONTINUE/ABORT/CHANGE_GOAL are consult-only and invalid at a normal step.
		s.retryCtx = RetryContext{Caller: nodeStep}
		return edgeLLMError, nil
	}
}
