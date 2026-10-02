package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

// taskCreatorFunc creates an agent task from a chat start_task intent and
// returns the new task id.
type taskCreatorFunc func(ctx context.Context, sessionID, goal string) (string, error)

// applyStartTask converts a start_task chat response into a created task.
// Falls back to ask_clarification when the goal is missing or creation fails.
func applyStartTask(ctx context.Context, resp conductor.ChatResponse, sessionID string, create taskCreatorFunc) conductor.ChatResponse {
	if resp.Intent != "start_task" || create == nil {
		return resp
	}
	goal := strings.TrimSpace(resp.TaskGoal)
	if goal == "" {
		goal = strings.TrimSpace(resp.Message)
	}
	if goal == "" {
		resp.Intent = "ask_clarification"
		resp.Message = "I need a clear goal to start a task. What should be accomplished?"
		return resp
	}
	taskID, err := create(ctx, sessionID, goal)
	if err != nil {
		resp.Intent = "decline"
		resp.Message = "Task planning failed: " + err.Error()
		return resp
	}
	resp.TaskID = taskID
	// A start_task response may still contain the model's draft single-command
	// proposals. The task runner owns execution from this point; surfacing the
	// drafts would let auto mode execute them a second time outside the task.
	resp.Proposals = nil
	resp.Warnings = nil
	return resp
}

func HandleChat(assistant *conductor.Engine, approvals *conductor.ApprovalStore, sm *session.Manager, taskRunner agent.TaskRunner, taskStore *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message   string `json:"message"`
			SessionID string `json:"session_id,omitempty"`
			TurnID    string `json:"turn_id,omitempty"`
			// FromFeedback marks an auto-generated turn (the UI feeding a
			// command's output back to the conductor). It still flows into the
			// model's in-memory context, but is NOT persisted to chat_messages —
			// otherwise a reloaded conversation would replay verbose synthetic
			// tool-output turns as if the operator had typed them.
			FromFeedback bool `json:"from_feedback,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		if len(req.Message) > 10000 {
			writeError(w, http.StatusBadRequest, "message is too long (max 10000 characters)", nil)
			return
		}
		if !checkOptionalSessionID(w, req.SessionID) {
			return
		}
		if req.TurnID == "" {
			req.TurnID = uuid.NewString()
		} else if _, err := uuid.Parse(req.TurnID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid turn_id", err)
			return
		}

		if assistant == nil {
			resp := conductor.ChatResponse{
				Intent:     "ask_clarification",
				Message:    "Assistant is not ready yet. Persona could not be loaded.",
				Confidence: 0.2,
			}
			sessionID := normalizeSessionID(req.SessionID)
			appendAuditSafe(sm, sessionID, "assistant.unavailable", "assistant", map[string]any{
				"message": req.Message,
			})
			writeJSON(w, http.StatusOK, map[string]any{
				"reply":      resp.Message,
				"session_id": sessionID,
				"turn_id":    req.TurnID,
				"data":       resp,
			})
			return
		}

		sessionID := normalizeSessionID(req.SessionID)

		// The UI uses "default" as a sentinel for ad-hoc chat state.
		// Only validate persisted sessions explicitly chosen by ID.
		if req.SessionID != "" && sessionID != "default" && sm != nil {
			if _, err := sm.Get(sessionID); err != nil {
				writeError(w, http.StatusNotFound, "session not found", nil)
				return
			}
		}

		// Restore prior conversation into the conductor's in-memory history
		// before this turn, so a process restart doesn't make the model answer
		// cold. Idempotent: SeedHistory only fills when the in-memory history is
		// empty (i.e. the first turn after a restart for this session).
		if sm != nil && sessionID != "" {
			if persisted, err := sm.ListMessages(sessionID, maxSeedMessages); err == nil && len(persisted) > 0 {
				assistant.SeedHistory(sessionID, toLLMHistory(persisted))
			}
		}
		if !req.FromFeedback {
			appendLatestTerminalTaskOutcome(assistant, taskStore, sessionID)
		}

		// 90s budget covers the default gpt-oss:20b on CPU-constrained hosts;
		// the server WriteTimeout (120s) leaves a 30s margin for response
		// serialisation.
		chatCtx, chatCancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer chatCancel()

		var resp conductor.ChatResponse
		if sessionID != "" {
			var turnErr error
			if req.FromFeedback {
				resp, turnErr = assistant.ChatWithHistoryFeedbackTurn(chatCtx, sessionID, req.TurnID, req.Message)
			} else {
				resp, turnErr = assistant.ChatWithHistoryTurn(chatCtx, sessionID, req.TurnID, req.Message)
			}
			if turnErr != nil {
				if errors.Is(turnErr, conductor.ErrTurnInProgress) {
					writeError(w, http.StatusConflict, "turn in progress", turnErr)
					return
				}
				writeError(w, http.StatusBadGateway, "chat turn failed", turnErr)
				return
			}
		} else {
			resp = assistant.Chat(chatCtx, req.Message)
		}

		// When the model decides the request needs a multi-step task, create
		// one (and plan it) via the agent runner. Single-command requests fall
		// through to the existing proposal/approval flow untouched.
		if taskRunner != nil {
			creator := func(ctx context.Context, sessionID, goal string) (string, error) {
				task, err := taskRunner.CreateAndPlan(ctx, sessionID, goal)
				if err != nil {
					return "", err
				}
				return task.ID, nil
			}
			resp = applyStartTask(chatCtx, resp, sessionID, creator)
		}

		// Start the task loop and return immediately. The UI tracks live
		// progress via the /ws/events WebSocket (task.status events).
		if resp.TaskID != "" && taskRunner != nil {
			if err := taskRunner.Start(resp.TaskID, ""); err != nil {
				slog.Warn("agent task start failed from chat", "task_id", resp.TaskID, "error", err)
				resp.Intent = "decline"
				if isLikelyTurkish(req.Message) {
					resp.Message = "Görev başlatılamadı: " + err.Error()
				} else {
					resp.Message = "Failed to start task: " + err.Error()
				}
			} else {
				resp.Intent = "explain"
				if isLikelyTurkish(req.Message) {
					resp.Message = "Görev başlatıldı. Plan ve adımlar aşağıda canlı olarak görüntüleniyor."
				} else {
					resp.Message = "Task started. Plan and progress are streaming live below."
				}
			}
		}

		approvalRequiredCount := 0
		for i := range resp.Proposals {
			resp.Proposals[i].ID = uuid.NewString()
			if resp.Proposals[i].RequiresApproval {
				approvalRequiredCount++
			}
			if approvals != nil {
				approvals.Put(sessionID, resp.Proposals[i])
			}
		}
		if approvalRequiredCount > 0 && approvals == nil {
			resp.Warnings = append(resp.Warnings, "approval store unavailable; cannot approve pending proposals")
		}
		appendAuditSafe(sm, sessionID, "assistant.chat", "assistant", map[string]any{
			"intent":                  resp.Intent,
			"message":                 req.Message,
			"proposal_count":          len(resp.Proposals),
			"approval_required_count": approvalRequiredCount,
			"confidence":              resp.Confidence,
		})

		// Persist this turn so the conversation survives a reload / restart /
		// session switch. Skip auto-feedback turns (kept out of the durable
		// thread on purpose) and the legacy "default" ad-hoc bucket. Best-effort
		// — a persistence failure never blocks the reply the operator already has.
		if sm != nil && sessionID != "" && sessionID != "default" && !req.FromFeedback {
			persistChatTurn(sm, sessionID, req.Message, resp)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"reply":                   resp.Message,
			"session_id":              sessionID,
			"turn_id":                 req.TurnID,
			"approval_required_count": approvalRequiredCount,
			"data":                    resp,
		})
	}
}

func appendLatestTerminalTaskOutcome(assistant *conductor.Engine, store *agent.Store, sessionID string) {
	if assistant == nil || store == nil || sessionID == "" {
		return
	}
	task, ok := latestCompletedTask(store, sessionID)
	if !ok {
		return
	}
	assistant.AppendTaskOutcome(sessionID, task.ID, task.Summary, task.Findings)
}

func latestCompletedTask(store *agent.Store, sessionID string) (agent.Task, bool) {
	if store == nil || sessionID == "" {
		return agent.Task{}, false
	}
	for _, task := range store.ListBySession(sessionID, 10) {
		if task.Status == agent.StatusCompleted {
			return task, true
		}
	}
	return agent.Task{}, false
}

// maxSeedMessages bounds how many persisted turns are pulled back into the
// conductor's in-memory history on restart. The conductor itself caps to
// maxHistoryPerSession; this is a DB-read ceiling a little above that.
const maxSeedMessages = 40

// toLLMHistory converts persisted chat turns into the conductor's message
// shape, keeping only the user/assistant turns that form LLM context (system
// rows are UI notices, not model turns).
func toLLMHistory(msgs []session.ChatMessage) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		out = append(out, llm.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// persistChatTurn durably records the operator's message and the assistant's
// reply. The assistant turn carries a small JSON payload (intent + proposals +
// task id) so the UI can rebuild a richer message on reload, not just plain
// text. All best-effort; failures are logged, never surfaced.
func persistChatTurn(sm *session.Manager, sessionID, userMessage string, resp conductor.ChatResponse) {
	if strings.TrimSpace(userMessage) != "" {
		if err := sm.AppendMessage(sessionID, "user", userMessage, ""); err != nil {
			slog.Warn("persist user chat message failed", "session", sessionID, "error", err)
		}
	}
	payload := ""
	if buf, err := json.Marshal(map[string]any{
		"intent":    resp.Intent,
		"proposals": resp.Proposals,
		"task_id":   resp.TaskID,
	}); err == nil {
		payload = string(buf)
	}
	if err := sm.AppendMessage(sessionID, "assistant", resp.Message, payload); err != nil {
		slog.Warn("persist assistant chat message failed", "session", sessionID, "error", err)
	}
}

func HandleChatApprove(approvals *conductor.ApprovalStore, executions *execution.Store, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if approvals == nil {
			writeError(w, http.StatusServiceUnavailable, "approval subsystem unavailable", nil)
			return
		}

		var req struct {
			SessionID  string `json:"session_id,omitempty"`
			ProposalID string `json:"proposal_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		if !checkOptionalSessionID(w, req.SessionID) {
			return
		}
		if req.ProposalID == "" {
			writeError(w, http.StatusBadRequest, "proposal_id is required", nil)
			return
		}
		if !isValidSessionID(req.ProposalID) {
			writeError(w, http.StatusBadRequest, "invalid proposal id format", nil)
			return
		}

		sessionID := normalizeSessionID(req.SessionID)
		item, err := approvals.Approve(sessionID, req.ProposalID)
		if err != nil {
			appendAuditSafe(sm, sessionID, "assistant.proposal.approve_failed", "user", map[string]any{
				"proposal_id": req.ProposalID,
				"error":       err.Error(),
			})
			writeError(w, http.StatusNotFound, "proposal not found or expired", err)
			return
		}
		// Audit the approval before attempting execution so we always have a
		// record of intent even if Enqueue fails.
		appendAuditSafe(sm, sessionID, "assistant.proposal.approved", "user", map[string]any{
			"proposal_id": req.ProposalID,
			"command":     item.Proposal.Command,
			"risk_level":  item.Proposal.RiskLevel,
			"mode":        "assistant_loop",
		})

		var executionPayload any = nil
		assistantMessage := "Proposal approved. Run in terminal."
		if executions != nil {
			ctx := execution.ContextWithRationale(r.Context(), item.Proposal.Rationale)
			rec, err := executions.Enqueue(ctx, sessionID, item.Proposal.ID, item.Proposal.Command, "assistant_loop")
			if err != nil {
				slog.Warn("approve: enqueue failed; falling back to manual terminal",
					"session", sessionID, "proposal", item.Proposal.ID, "error", err)
				appendAuditSafe(sm, sessionID, "assistant.proposal.enqueue_failed", "system", map[string]any{
					"proposal_id": req.ProposalID,
					"error":       err.Error(),
				})
			} else {
				executionPayload = map[string]any{
					"id":     rec.ID,
					"status": rec.Status,
				}
				assistantMessage = "Approved. Running in sandbox; result will appear in chat shortly."
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"reply": localApprovalMessage(item.Proposal),
			"data": map[string]any{
				"intent":  "suggest_command",
				"message": assistantMessage,
				"proposal": map[string]any{
					"id":                item.Proposal.ID,
					"command":           item.Proposal.Command,
					"risk_level":        item.Proposal.RiskLevel,
					"requires_approval": false,
				},
				"execution": executionPayload,
			},
		})
	}
}

func isLikelyTurkish(s string) bool {
	lower := strings.ToLower(s)
	if strings.ContainsAny(lower, "çğıöşü") {
		return true
	}
	for _, k := range []string{"lütfen", "hedef", "tarama", "tara", "için", "ve", "nasıl"} {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}
