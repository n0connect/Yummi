package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
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
	return resp
}

func HandleChat(assistant *conductor.Engine, approvals *conductor.ApprovalStore, sm *session.Manager, taskRunner *agent.Runner, taskStore *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message   string `json:"message"`
			SessionID string `json:"session_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
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

		// 90s budget covers qwen3:14b on CPU-constrained hosts; the server
		// WriteTimeout (120s) leaves a 30s margin for response serialisation.
		chatCtx, chatCancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer chatCancel()

		var resp conductor.ChatResponse
		if sessionID != "" {
			resp = assistant.ChatWithHistory(chatCtx, sessionID, req.Message)
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
			if err := taskRunner.Start(resp.TaskID); err != nil {
				slog.Warn("agent task start failed from chat", "task_id", resp.TaskID, "error", err)
				resp.Intent = "decline"
				resp.Message = "Görev başlatılamadı: " + err.Error()
			} else {
				resp.Intent = "explain"
				resp.Message = "Görev başlatıldı. Plan ve adımlar aşağıda canlı olarak görüntüleniyor."
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

		writeJSON(w, http.StatusOK, map[string]any{
			"reply":                   resp.Message,
			"session_id":              sessionID,
			"approval_required_count": approvalRequiredCount,
			"data":                    resp,
		})
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
		if req.ProposalID == "" {
			writeError(w, http.StatusBadRequest, "proposal_id is required", nil)
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
			rec, err := executions.Enqueue(sessionID, item.Proposal.ID, item.Proposal.Command, "assistant_loop")
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
				assistantMessage = "Approved. Running in n0; result will appear in chat shortly."
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

func HandleEventsWS(executions *execution.Store, tasks *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if executions == nil {
			http.Error(w, "execution store unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("events websocket upgrade failed", "error", err)
			return
		}
		defer func() { _ = conn.Close() }()

		sessionFilter := normalizeSessionID(r.URL.Query().Get("session_id"))
		if r.URL.Query().Get("session_id") == "" {
			sessionFilter = ""
		}

		subID, events, err := executions.Subscribe(64)
		if err != nil {
			slog.Warn("events subscribe failed", "error", err)
			_ = conn.WriteJSON(map[string]any{
				"type": "error",
				"data": map[string]any{"message": err.Error()},
			})
			return
		}
		defer executions.Unsubscribe(subID)

		// Task events share this websocket. nil task store (e.g. LLM disabled)
		// simply means no task channel — execution events still flow.
		var taskEvents <-chan agent.Event
		if tasks != nil {
			taskSubID, ch, terr := tasks.Subscribe(64)
			if terr == nil {
				taskEvents = ch
				defer tasks.Unsubscribe(taskSubID)
			}
		}

		pingTicker := time.NewTicker(20 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case evt, ok := <-events:
				if !ok {
					return
				}
				if sessionFilter != "" && evt.Record.SessionID != sessionFilter {
					continue
				}
				payload := map[string]any{
					"type":      "execution.status",
					"event":     evt.Type,
					"timestamp": evt.Timestamp,
					"data":      evt.Record,
				}
				if err := conn.WriteJSON(payload); err != nil {
					return
				}
			case evt, ok := <-taskEvents:
				if !ok {
					taskEvents = nil
					continue
				}
				if sessionFilter != "" && evt.Task.SessionID != sessionFilter {
					continue
				}
				payload := map[string]any{
					"type":      "task.status",
					"event":     evt.Type,
					"timestamp": evt.Timestamp,
					"data":      evt.Task,
				}
				if err := conn.WriteJSON(payload); err != nil {
					return
				}
			case <-pingTicker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(2*time.Second)); err != nil {
					return
				}
			}
		}
	}
}
