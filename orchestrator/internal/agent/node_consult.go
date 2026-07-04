package agent

import "context"

// consultNode handles a still-running command that passed its soft timeout. It
// asks the model whether to keep waiting; any answer other than an explicit
// CONTINUE (or STOP_TASK) cancels the command and returns control to the step
// node so the model can decide its next move normally. It never dead-ends on a
// non-control reply.
func consultNode(ctx context.Context, s *GraphState) (string, error) {
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}
	system := StepSystemPrompt(s.cfg.Persona)
	prompt := BuildStepPrompt(task, Wrap(s.consultTag, s.consultBody), s.memCtx.Summary, s.buildPreamble(ctx, task))

	raw, err := s.llm.Chat(ctx, system, prompt)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.retryCtx = RetryContext{Caller: nodeConsult, Attempts: s.retryCtx.Attempts}
		return edgeLLMError, nil
	}
	msg, perr := ParseControl(raw)

	// Explicit "keep waiting".
	if perr == nil && msg.Tag == "CONTINUE" {
		if s.pendingHandle != nil {
			result, done := s.pendingHandle.Wait(s.cfg.SoftTimeout)
			if !done {
				return edgeLongRunning, nil // still running → consult again
			}
			s.pendingHandle = nil
			return observeResult(s, result), nil
		}
		return edgeContinue, nil
	}

	// Explicit stop the whole task.
	if perr == nil && msg.Tag == "STOP_TASK" {
		if s.pendingHandle != nil {
			s.pendingHandle.Cancel()
			s.pendingHandle = nil
		}
		s.finishOutcome = outcomeStopped
		s.finishMsg = orDefault(msg.Action.Reason, "stopped by model")
		return edgeStopTask, nil
	}

	// Anything else (ABORT, a fresh action, or an unparseable reply): cancel the
	// long-running command and let the model pick its next move at the step node.
	if s.pendingHandle != nil {
		s.pendingHandle.Cancel()
		s.pendingHandle = nil
	}
	obs := Wrap("TOOL_FAILED", "the long-running command `"+s.pendingCommand+
		"` was canceled so you can proceed. Choose your next action.")
	s.memCtx.AddObservation(obs)
	s.lastObs = obs
	// Every other termination path (success, failure, gateway-block) marks the
	// plan step via markStepStatus; an aborted long-running command must too,
	// or the plan view is stuck showing "pending" forever even after the task
	// completes — live-observed on a real task where nikto ran long, got
	// aborted, and the finished task's plan looked like nothing had happened.
	markStepStatus(s.store, s.taskID, s.pendingAct.StepID, StepFailed, "command aborted after running too long")
	return edgeAbort, nil
}
