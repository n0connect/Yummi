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
		return s.blockCommand(Wrap("COMMAND_BLOCKED", reason+
			"\nThe command was empty or malformed after normalization. Propose a concrete, well-formed command."))
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
		// or finish). Keep the current command as the new baseline: clearing the
		// window entirely would let the exact same command execute again after
		// every third blocked proposal.
		obs := Wrap("ABNORMAL_PATTERN", "You are repeating the same commands. Recent: ["+
			strings.Join(s.recentCmds, ", ")+"]. STOP repeating — choose a DIFFERENT command, "+
			"update_plan with a new approach, or finish if the goal is already met.")
		s.cmdWindow = []string{hash}
		s.recentCmds = []string{p.Command}
		return s.blockCommand(obs)
	}
	// Block re-running any command already seen in the recent window — not just
	// the immediately-previous one. This catches A→B→A→B style oscillation at
	// the first repeat instead of letting it cycle until stuck detection.
	if windowCount(s.cmdWindow, hash) >= 2 {
		return s.blockCommand(Wrap("DUPLICATE_BLOCKED", "You already ran this exact command recently: `"+p.Command+"`\n"+
			"Re-running it produces the same output you already have. Choose a DIFFERENT command, "+
			"update the plan, or finish if the goal is met."))
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
		s.cmdWindow = nil
		s.recentCmds = nil
		return s.blockCommand(obs)
	}

	// The command passed validation, stuck, and duplicate checks — it is about
	// to run (now in auto mode, or after approval). Surface it on the timeline.
	// Exact command + model rationale already have their dedicated audited
	// records. Activity is a public progress surface, so keep it semantic.
	s.emit(ActCommand, "Command dispatched", p.Rationale)

	// Approval is mode-driven, not risk-driven: in ask_permission mode EVERY
	// command gates on the operator; in auto mode none do — with exactly one
	// hard exception. ForceApprovalReason is set only for a small set of
	// known irreversible/destructive command shapes (validator.go's
	// destructiveFloor: rm -rf, dd to a device, mkfs, shred) that classifyRisk's
	// shell-shape allowlist and the model's own risk_level both can miss.
	// Auto mode exists so an operator can let ordinary risk run unattended —
	// it was never meant to also mean "run a disk-wipe unsupervised because a
	// small local model misjudged it as low risk."
	//
	// task.AutoApproveNonCritical (see its own doc comment on Task) is a
	// narrower, per-task, operator-opted-in escape from ask_permission mode
	// specifically — it never touches ForceApprovalReason's hard gate, and
	// only ever skips the gate for a risk level below high/critical.
	skipForTaskOverride := task.AutoApproveNonCritical && p.ForceApprovalReason == "" && !conductor.IsHighSeverity(p.RiskLevel)
	if (s.cfg.ApprovalMode == ModeAskPermission && !skipForTaskOverride) || p.ForceApprovalReason != "" {
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
	s.executedCmds = append(s.executedCmds, p.Command)
	result, done := handle.Wait(s.cfg.SoftTimeout)
	if !done {
		s.pendingHandle = handle
		s.consultCaller = nodeTool
		// Distinguish a genuinely slow command from one blocked on operator
		// egress approval (gateway Ask mode). The latter would otherwise be
		// reported as "still running", which is misleading — the command isn't
		// progressing, it's waiting for the operator to approve the external
		// target in the egress bar.
		if target, held := handle.EgressHold(); held {
			s.consultTag = "AWAITING_EGRESS_APPROVAL"
			s.consultBody = fmt.Sprintf("Command `%s` is held awaiting your egress approval for %q. Approve or deny it in the egress bar, then reply [CONTINUE] to keep waiting, or [ABORT] to cancel.",
				p.Command, target)
		} else {
			s.consultTag = "LONG_RUNNING"
			s.consultBody = fmt.Sprintf("Command still running: `%s` (~%.0fs elapsed). Reply [CONTINUE] to keep waiting or [ABORT] to cancel.",
				p.Command, s.cfg.SoftTimeout.Seconds())
		}
		return edgeLongRunning, nil
	}
	return observeResult(s, result), nil
}

// blockCommand records msg as both the rolling memory observation and the
// node's immediate lastObs (what the next step prompt shows), then routes to
// the error edge — the shared outcome for every toolNode guard (validator
// rejection, stuck/duplicate/thrash detection) that rejects a command before
// it ever runs. Any window reset the caller needs must happen BEFORE calling
// this, since it only handles the observation + edge, not the windows.
func (s *GraphState) blockCommand(msg string) (string, error) {
	s.memCtx.AddObservation(msg)
	s.lastObs = msg
	return edgeIsError, nil
}
