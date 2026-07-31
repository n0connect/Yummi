package agent

import (
	"context"
	"time"
)

// threatHoldNode pauses the loop when a command output trips known threat scan
// patterns. It buffers the output in s.pendingThreatResult and blocks the execution
// loop on the operator gate. The operator can approve the raw output, deny/discard
// it, or supply a redacted version.
func threatHoldNode(ctx context.Context, s *GraphState) (string, error) {
	p := s.pendingProposal
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}

	cmdLabel := "[THREAT_HOLD] " + s.pendingCommand
	verdictCh := s.gate.Request(s.taskID, p.ID, cmdLabel, p.RiskLevel)
	if err := s.store.Transition(s.taskID, StatusAwaitingApproval, func(t *Task) {
		t.PendingProposalID = p.ID
		t.PendingCommand = cmdLabel
		t.PendingRisk = p.RiskLevel
	}); err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()

	case <-time.After(s.cfg.ApprovalTimeout):
		s.gate.Clear(s.taskID)
		timeoutMsg := "Threat scan approval timed out for command: " + s.pendingCommand
		_ = s.store.Transition(s.taskID, StatusPaused, func(t *Task) {
			t.Question = timeoutMsg
			t.PendingProposalID, t.PendingCommand, t.PendingRisk = "", "", ""
		})
		s.pendingAct = Action{Question: timeoutMsg}
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
				reason = "Operator discarded command output due to detected threat."
			}
			obs := Wrap("TOOL_FAILED", s.pendingCommand+" — output blocked: "+reason)
			s.memCtx.AddObservation(obs)
			s.lastObs = obs
			markStepStatus(s.store, s.taskID, s.pendingAct.StepID, s.pendingCommand, StepFailed, reason)
			return edgeDenied, nil
		}

		// Operator approved or redacted it!
		output := s.pendingThreatResult.Content
		if v.Reason != "" {
			// Redacted content is supplied in Reason.
			output = v.Reason
		}

		compressed := CompressObservation(output)
		tag := "TOOL_RESPONSE"
		if compressed != output {
			tag = "TOOL_RESPONSE_TRUNCATED"
		}
		body := s.pendingCommand + "\n" + compressed
		obs := Wrap(tag, body)

		// Persist approved/redacted observation to memory context.
		s.memCtx.AddObservation(output)
		s.lastObs = obs
		markStepStatus(s.store, s.taskID, s.pendingAct.StepID, s.pendingCommand, StepDone, obs)
		s.emit(ActCommandDone, "Command result processed", compressed)

		// Process findings associated with this command step.
		if findings := sanitizeFindings(s.pendingAct.Findings); len(findings) > 0 || len(s.pendingAct.FindingsDetail) > 0 {
			// Re-fetch: task was snapshotted before the operator gate wait
			// above, so its Plan may be stale by now.
			var records []FindingRecord
			if fresh, ok := s.store.Get(s.taskID); ok {
				records = buildFindingRecords(s.pendingAct.FindingsDetail, s.pendingAct.StepID, output, fresh.Plan)
			}
			_ = s.store.Update(s.taskID, func(t *Task) {
				t.Findings = append(t.Findings, findings...)
				t.FindingRecords = append(t.FindingRecords, records...)
			})
			recordTaskFindings(ctx, s.cfg.Knowledge, task.SessionID, s.taskID, findings, s.pendingAct.FindingsDetail)
			for _, f := range findings {
				s.emit(ActFinding, f, "")
			}
			for _, d := range s.pendingAct.FindingsDetail {
				if d.Title != "" {
					s.emit(ActFinding, sanitizeAgentText(d.Title), sanitizeAgentText(d.Description))
				}
			}
		}

		return edgeApproved, nil
	}
}
