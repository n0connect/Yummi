package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

// toolNode validates, gates, and executes the buffered command. It detects
// stuck patterns and prior failures, and supervises long-running commands via
// the consult channel.
func toolNode(ctx context.Context, s *GraphState) (string, error) {
	action := s.pendingAct
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}

	proposal := conductor.Proposal{
		ID:        uuid.NewString(),
		Command:   action.Command,
		Rationale: action.Rationale,
		RiskLevel: action.RiskLevel,
	}
	validated := s.validate(proposal)
	if len(validated.Proposals) == 0 {
		reason := strings.Join(validated.Warnings, "; ")
		if reason == "" {
			reason = "command rejected by validator"
		}
		obs := Wrap("COMMAND_BLOCKED", reason+
			"\nThe command was empty or malformed after normalization. Propose a concrete, well-formed command.")
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		return edgeIsError, nil
	}
	p := validated.Proposals[0]
	s.pendingCommand = p.Command

	// Stuck detection before execution.
	hash := commandHash(p.Command)
	s.cmdWindow = pushWindow(s.cmdWindow, hash, stuckWindowSize)
	s.recentCmds = pushWindow(s.recentCmds, p.Command, stuckWindowSize)
	if isStuck(s.cmdWindow) {
		// Surface the loop to the model as an observation and let it choose its
		// next move normally at the step node (a different command, update_plan,
		// or finish). The window is reset so the warning is a clean restart and
		// does not immediately re-fire.
		obs := Wrap("ABNORMAL_PATTERN", "You are repeating the same commands. Recent: ["+
			strings.Join(s.recentCmds, ", ")+"]. STOP repeating — choose a DIFFERENT command, "+
			"update_plan with a new approach, or finish if the goal is already met.")
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		s.cmdWindow = nil
		s.recentCmds = nil
		return edgeIsError, nil
	}
	// Block re-running any command already seen in the recent window — not just
	// the immediately-previous one. This catches A→B→A→B style oscillation at
	// the first repeat instead of letting it cycle until stuck detection.
	if windowCount(s.cmdWindow, hash) >= 2 {
		obs := Wrap("DUPLICATE_BLOCKED", "You already ran this exact command recently: `"+p.Command+"`\n"+
			"Re-running it produces the same output you already have. Choose a DIFFERENT command, "+
			"update the plan, or finish if the goal is met.")
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		return edgeIsError, nil
	}
	// Tool-level thrash: the model is rotating trivially-different invocations of
	// the SAME tool (ps -ef / ps -ejH / ps aux), which exact-hash detection above
	// misses. Nudge it to stop re-querying the same thing in different forms and
	// either analyze what it already has or move on. Non-blocking: the model is
	// told, not silenced. Reset the window so the nudge restarts cleanly.
	if tool, thrashing := toolThrash(s.recentCmds); thrashing {
		obs := Wrap("ABNORMAL_PATTERN", "You have run `"+tool+"` "+
			"several times with only trivially-different flags. These produce the SAME class of "+
			"information you already have — you are not learning anything new. STOP re-querying with "+
			"`"+tool+"`: analyze the output you already collected, switch to a DIFFERENT tool/approach, "+
			"update_plan, or finish if the goal is met.")
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		s.cmdWindow = nil
		s.recentCmds = nil
		return edgeIsError, nil
	}

	// The command passed validation, stuck, and duplicate checks — it is about
	// to run (now in auto mode, or after approval). Surface it on the timeline.
	s.emit(ActCommand, p.Command, p.Rationale)

	// Approval is mode-driven, not risk-driven: in ask_permission mode EVERY
	// command gates on the operator; in auto mode none do.
	if s.cfg.ApprovalMode == ModeAskPermission {
		s.pendingProposal = p
		return edgeRequiresApproval, nil
	}

	// Supervised execution: wait up to SoftTimeout, then consult on long-running.
	handle, err := s.exec.Start(ctx, task.SessionID, p.Command)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("exec start: %w", err)
	}
	result, done := handle.Wait(s.cfg.SoftTimeout)
	if !done {
		s.pendingHandle = handle
		s.consultTag = "LONG_RUNNING"
		s.consultBody = fmt.Sprintf("Command still running: `%s` (~%.0fs elapsed). Reply [CONTINUE] to keep waiting or [ABORT] to cancel.",
			p.Command, s.cfg.SoftTimeout.Seconds())
		s.consultCaller = nodeTool
		return edgeLongRunning, nil
	}
	return observeResult(s, result), nil
}
