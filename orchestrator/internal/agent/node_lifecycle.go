package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// retryNode backs off after an LLM error, then re-enters the calling node.
func retryNode(ctx context.Context, s *GraphState) (string, error) {
	cfg := s.cfg.Retry
	if cfg.MaxAttempts <= 0 {
		cfg = defaultRetryConfig
	}
	s.retryCtx.Attempts++

	if s.retryCtx.Exhausted(cfg) {
		msg := fmt.Sprintf("LLM unavailable after %d retries; pausing for operator.", cfg.MaxAttempts)
		s.lastErr = msg
		s.pendingAct = Action{Question: msg}
		return edgeGiveUp, nil
	}

	delay := cfg.Delay(s.retryCtx.Attempts)
	slog.Info("agent: LLM retry", "task", s.taskID, "attempt", s.retryCtx.Attempts, "delay", delay)
	_ = s.store.Transition(s.taskID, StatusRetryingLLM, nil)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(delay):
	}

	_ = s.store.Transition(s.taskID, StatusRunning, nil)
	return edgeRetryAgain, nil
}

// approvalNode blocks on the operator gate for medium+ risk commands.
func approvalNode(ctx context.Context, s *GraphState) (string, error) {
	p := s.pendingProposal
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}

	verdictCh := s.gate.Request(s.taskID, p.ID, p.Command, p.RiskLevel)
	if err := s.store.Transition(s.taskID, StatusAwaitingApproval, func(t *Task) {
		t.PendingProposalID = p.ID
		t.PendingCommand = p.Command
		t.PendingRisk = p.RiskLevel
	}); err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()

	case <-time.After(s.cfg.ApprovalTimeout):
		s.gate.Clear(s.taskID)
		// Operator-facing pause message (plain text, not a protocol tag).
		timeout := "Approval timed out for: " + p.Command
		_ = s.store.Transition(s.taskID, StatusPaused, func(t *Task) {
			t.Question = timeout
			t.PendingProposalID, t.PendingCommand, t.PendingRisk = "", "", ""
		})
		s.pendingAct = Action{Question: timeout}
		return edgeGiveUp, nil

	case v := <-verdictCh:
		if err := s.store.Transition(s.taskID, StatusRunning, func(t *Task) {
			t.PendingProposalID, t.PendingCommand, t.PendingRisk = "", "", ""
		}); err != nil {
			return "", err
		}
		if !v.Approved {
			reason := v.Reason
			if reason == "" {
				reason = "no reason given"
			}
			obs := Wrap("APPROVAL_DENIED", p.Command+" — reason: "+reason+"\nPropose an alternative or finish.")
			s.memCtx.AddObservation(obs)
			s.lastObs = obs
			return edgeDenied, nil
		}
		// Execute the approved command and observe it like any other.
		s.pendingCommand = p.Command
		result, err := s.exec.Run(ctx, task.SessionID, p.Command)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("exec (post-approval): %w", err)
		}
		s.executedCmds = append(s.executedCmds, p.Command)
		_ = observeResult(s, result)
		return edgeApproved, nil
	}
}

// summarizeNode compresses observation history into a running summary when the
// token budget is under pressure.
func summarizeNode(ctx context.Context, s *GraphState) (string, error) {
	if len(s.memCtx.Observations) == 0 {
		return edgeDone, nil
	}
	// Protect the FIRST observation (it usually establishes the target, topology,
	// and open ports — losing it costs the model its grounding) plus as much of
	// the recent tail as fits, compressing only the middle. A plain tail-cut would
	// drop the early recon entirely. (hermes trajectory_compressor strategy.)
	joined := summarizeInput(s.memCtx.Observations, 8000)

	inputToSummarize := ""
	if s.memCtx.Summary != "" {
		inputToSummarize = "PREVIOUS SUMMARY:\n" + s.memCtx.Summary + "\n\nNEW OBSERVATIONS:\n" + joined
	} else {
		inputToSummarize = "OBSERVATIONS:\n" + joined
	}

	system := "You are a penetration testing AI. Summarize prior tool observations and context concisely."
	user := "Combine the previous summary (if any) and new observations into a single consolidated summary of ≤400 words. " +
		"Ensure you preserve all crucial technical details: target IPs, active ports, discovered credentials, paths/URLs, and vulnerabilities.\n\n" +
		inputToSummarize

	_ = s.store.Transition(s.taskID, StatusSummarizing, nil)

	summary, err := s.llm.Chat(ctx, system, user)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		slog.Warn("agent: summarize LLM error; continuing without compression", "task", s.taskID, "error", err)
		_ = s.store.Transition(s.taskID, StatusRunning, nil)
		// Non-fatal: reset token counter and keep going.
		s.memCtx.TokensUsed = 0
		return edgeDone, nil
	}

	s.memCtx.Summary = summary
	s.memCtx.TokensUsed = 0
	s.memCtx.Observations = nil

	_ = s.store.Transition(s.taskID, StatusRunning, func(t *Task) {
		t.Checkpoint.Summary = summary
		t.Checkpoint.TokensUsed = 0
	})
	return edgeDone, nil
}

// summarizeInput assembles the text fed to the summarizer, preserving the first
// observation (initial grounding) and the most recent tail while compressing the
// middle to fit budget chars. A naive tail-cut would discard the early recon —
// target, topology, open ports — that the model most needs to keep. When the
// whole history already fits, it returns it verbatim.
func summarizeInput(obs []string, budget int) string {
	const sep = "\n---\n"
	joined := strings.Join(obs, sep)
	if len(joined) <= budget {
		return joined
	}
	first := obs[0]
	// Reserve up to a third of the budget for the first observation so a single
	// huge banner can't crowd out the recent tail.
	firstCap := budget / 3
	if len(first) > firstCap {
		first = first[:utf8SafeCut(first, firstCap)] + "…(truncated)"
	}
	const omitted = "\n---\n[…older observations omitted to fit context…]\n---\n"
	tailBudget := max(budget-len(first)-len(omitted), 0)
	tail := joined
	if len(tail) > tailBudget {
		tail = tail[utf8SafeCut(tail, len(tail)-tailBudget):]
	}
	return first + omitted + tail
}

// finishNode terminates the task: completed, failed, or stopped. Whenever a
// usable summary is missing (the model looped without finishing, or ran out of
// budget), a final synthesis pass turns the gathered observations into a report
// + findings so the operator always gets the requested output, not an empty
// result. Weak models often never emit FINISH on their own — this guarantees a
// deliverable regardless.
func finishNode(ctx context.Context, s *GraphState) (string, error) {
	switch s.finishOutcome {
	case outcomeCompleted:
		summary := sanitizeAgentText(s.finishMsg)
		findings := sanitizeFindings(s.pendingAct.Findings)
		if strings.TrimSpace(summary) == "" {
			summary, findings = mergeSynthesis(ctx, s, findings)
		}
		summary = appendUnverifiedToolWarning(s, summary, findings)
		summary = appendEvidenceDigest(s, summary)
		// FINISH isn't tied to one just-run step, so there is no live output
		// to check against — verification here can only look up whichever
		// step each finding cites in the plan as it stands right now.
		var records []FindingRecord
		if task0, ok := s.store.Get(s.taskID); ok {
			records = buildFindingRecords(s.pendingAct.FindingsDetail, "", "", task0.Plan)
		}
		_ = s.store.Transition(s.taskID, StatusCompleted, func(t *Task) {
			t.Summary = summary
			t.Findings = findings
			t.FindingRecords = append(t.FindingRecords, records...)
		})
		if task, ok := s.store.Get(s.taskID); ok {
			recordTaskFindings(ctx, s.cfg.Knowledge, task.SessionID, s.taskID, findings, s.pendingAct.FindingsDetail)
			proposeSkillFromTrajectory(s, task, summary)
		}
		s.emit(ActFinish, summary, "")
	case outcomeStopped:
		_ = s.store.Transition(s.taskID, StatusStopped, func(t *Task) {
			t.Summary = s.finishMsg
		})
	default: // outcomeFailed
		summary, findings := mergeSynthesis(ctx, s, nil)
		summary = appendUnverifiedToolWarning(s, summary, findings)
		summary = appendEvidenceDigest(s, summary)
		_ = s.store.Transition(s.taskID, StatusFailed, func(t *Task) {
			t.LastError = s.finishMsg
			t.Summary = summary // what was gathered before the failure, if anything
			if len(findings) > 0 {
				t.Findings = findings
			}
		})
	}
	return edgeDone, nil
}

// appendUnverifiedToolWarning looks up the task's current plan and appends
// unverifiedToolWarning's caveat to summary when findings reference a tool
// whose step failed. Shared by both the completed and failed outcomes.
func appendUnverifiedToolWarning(s *GraphState, summary string, findings []string) string {
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return summary
	}
	if warning := unverifiedToolWarning(task, findings); warning != "" {
		return strings.TrimSpace(summary + "\n\n" + warning)
	}
	return summary
}

// appendEvidenceDigest adds a bounded, server-authored audit trail to the
// model's prose. This guarantees that a vague synthesis cannot hide the exact
// commands and observations that support (or limit) the final answer.
func appendEvidenceDigest(s *GraphState, summary string) string {
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return summary
	}
	var lines []string
	for _, step := range task.Plan {
		if step.Status != StepDone && step.Status != StepFailed {
			continue
		}
		detail := step.Result
		if step.Status == StepFailed {
			detail = step.Error
		}
		detail = sanitizeAgentText(detail)
		if len(detail) > 320 {
			detail = detail[:utf8SafeCut(detail, 320)] + "…"
		}
		line := fmt.Sprintf("- [%s] %s", step.Status, strings.TrimSpace(step.Command))
		if detail != "" {
			line += "\n  " + strings.ReplaceAll(detail, "\n", "\n  ")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return summary
	}
	digest := "EVIDENCE DIGEST (server-recorded):\n" + strings.Join(lines, "\n")
	if strings.TrimSpace(summary) == "" {
		return digest
	}
	return "MODEL NARRATIVE (unverified; evidence digest is authoritative):\n" + strings.TrimSpace(summary) + "\n\n" + digest
}

// proposeSkillFromTrajectory fires the optional auto-skill-creation hook,
// The callback is expected to be a non-blocking submission into the bounded
// skillcurator.Worker. Fired for both top-level and sub-agent completions —
// finishNode is shared by both.
func proposeSkillFromTrajectory(s *GraphState, task Task, summary string) {
	if s.cfg.SkillCurator == nil {
		return
	}
	traj := skillcurator.Trajectory{
		TaskID:       s.taskID,
		SessionID:    task.SessionID,
		Goal:         task.Goal,
		Summary:      summary,
		Observations: append([]string{}, s.memCtx.Observations...),
	}
	if s.cfg.Knowledge != nil {
		for _, tool := range toolsFromCommands(s.recentCmds) {
			for _, tac := range s.cfg.Knowledge.ListTactics(tool, 3) {
				if tac.Outcome == "worked" {
					traj.Tactics = append(traj.Tactics, tac)
				}
			}
		}
	}
	s.cfg.SkillCurator(context.WithoutCancel(context.Background()), traj)
}

// mergeSynthesis runs the final-synthesis pass and merges any synthesized
// findings with the caller's existing ones.
func mergeSynthesis(ctx context.Context, s *GraphState, existing []string) (string, []string) {
	summary, _ := synthesizeSummary(ctx, s)
	// Fallback synthesis is allowed to explain observations, but it cannot
	// mint findings because it provides no evidence-bearing FindingDetail.
	// Findings already produced by the normal protocol remain intact.
	return summary, existing
}

// synthesizeSummary makes one LLM call over the gathered observations to produce
// a concise report + concrete findings. Returns empty on no LLM / no
// observations / failure (never blocks termination).
func synthesizeSummary(ctx context.Context, s *GraphState) (string, []string) {
	if s.llm == nil {
		return "", nil
	}
	body := strings.Join(s.memCtx.Observations, "\n---\n")
	if s.memCtx.Summary != "" {
		body = s.memCtx.Summary + "\n---\n" + body
	}
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	if len(body) > 8000 {
		body = body[len(body)-8000:]
	}
	system := s.cfg.Persona.Identity
	user := "The task is ending. Using ONLY the observations below, write a concise report " +
		"(≤200 words) of what was accomplished and concretely what was found. Be strictly " +
		"truthful: report only what the observations actually show. If something was not found, " +
		"not present, not installed, or could not be determined, say so plainly — do NOT claim a " +
		"detection or success that did not occur. Reply ONLY with this JSON: " +
		"{\"summary\":\"...\",\"findings\":[\"...\"]}\n\nOBSERVATIONS:\n" + body
	raw, err := s.llm.Chat(ctx, system, user)
	if err != nil {
		return "", nil
	}
	var out struct {
		Summary  string   `json:"summary"`
		Findings []string `json:"findings"`
	}
	_ = json.Unmarshal([]byte(extractJSON(stripThinkTokens(raw))), &out)
	return sanitizeAgentText(out.Summary), sanitizeFindings(out.Findings)
}

// askNode pauses the task for operator input (plain operator-facing text).
func askNode(ctx context.Context, s *GraphState) (string, error) {
	question := sanitizeAgentText(s.pendingAct.Question)
	if question == "" {
		question = "The agent needs operator input to continue."
	}
	_ = s.store.Transition(s.taskID, StatusPaused, func(t *Task) {
		t.Question = question
		if s.lastErr != "" {
			t.LastError = s.lastErr
		}
	})
	s.emit(ActAsk, question, "")
	return edgeDone, nil
}
