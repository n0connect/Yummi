// Package api defines the HTTP routes and handlers for orchestrator.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/config"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/health"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/session"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// NewRouter creates the Chi router with all routes.
func NewRouter(sm *session.Manager, ks *knowledge.Store, skillReg *skill.Registry, cfg *config.Config, assistant *conductor.Engine, approvals *conductor.ApprovalStore, executions *execution.Store, bridge *sandbox.Bridge, pc *gateway.Client, lb *LogBuffer, llmClient *llm.Client, agg *health.Aggregator, taskStore *agent.Store, taskRunner *agent.Runner) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(LoggingMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(TimeoutUnlessWebSocket(cfg.RequestTimeout()))
	r.Use(CORSMiddleware)

	// Health check (container probes, CLI readiness)
	r.Get("/healthz", HandleHealthz)

	// REST API
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(JSONContentType)
		r.Use(BodyLimit)

		// Sessions
		r.Get("/sessions", HandleListSessions(sm))
		r.Post("/sessions", HandleCreateSession(sm))
		r.Get("/sessions/{id}", HandleGetSession(sm))
		r.Get("/sessions/{id}/messages", HandleListMessages(sm))
		r.Patch("/sessions/{id}", HandleUpdateSession(sm))
		r.Delete("/sessions/{id}", HandleDeleteSession(sm))

		// Targets (owned by the knowledge store)
		r.Get("/sessions/{sessionID}/targets", HandleListTargets(ks))
		r.Post("/sessions/{sessionID}/targets", HandleCreateTarget(ks))

		// Findings (owned by the knowledge store)
		r.Get("/sessions/{sessionID}/findings", HandleListFindings(ks))
		r.Post("/sessions/{sessionID}/findings", HandleCreateFinding(ks))

		// TRACE observability — read-only views of the agent's world model.
		r.Get("/sessions/{sessionID}/activity", HandleListActivity(taskStore))
		r.Get("/sessions/{sessionID}/context", HandleListContext(ks))
		r.Get("/tactics", HandleListTactics(ks))
		r.Get("/skills", HandleListSkills(skillReg))
		r.Get("/agent/roles", HandleListRoles(taskRunner))
		r.Get("/sessions/{sessionID}/audit", HandleListAudit(sm))
		r.Get("/sessions/{sessionID}/executions", HandleListExecutions(executions))
		r.Get("/executions/{id}", HandleGetExecution(executions))
		r.Post("/executions/{id}/cancel", HandleCancelExecution(executions, sm))
		r.Post("/executions/{id}/resolve-egress", HandleResolveEgress(executions, pc, sm))

		// gateway status
		r.Get("/gateway/status", HandleGatewayStatus(pc))

		// gateway rule management & audit log
		r.Get("/gateway/rules", HandleGatewayRules(pc))
		r.Post("/gateway/rules", HandleGatewayAddRule(pc))
		r.Delete("/gateway/rules/{id}", HandleGatewayRemoveRule(pc))
		r.Get("/gateway/audit", HandleGatewayAudit(pc))
		r.Get("/gateway/fingerprint", HandleGatewayFingerprintConfig(pc))
		r.Get("/gateway/egress-mode", HandleGatewayEgressMode(pc))
		r.Post("/gateway/egress-mode", HandleGatewaySetEgressMode(pc))

		// sandbox status & direct exec
		r.Get("/sandbox/status", HandleSandboxStatus(bridge))
		r.Post("/sandbox/exec", HandleSandboxExec(bridge, pc, sm))

		// System aggregated status, metrics & debug log stream
		r.Get("/system/status", HandleSystemStatus(bridge, pc, cfg.DockerSocket, assistant))
		r.Get("/system/health-metrics", HandleHealthMetrics(agg))
		r.Get("/system/logs", HandleSystemLogs(lb))
		r.Get("/llm/status", HandleLLMStatus(llmClient))

		// Chat
		r.Post("/chat", HandleChat(assistant, approvals, sm, taskRunner, taskStore))
		r.Post("/chat/approve", HandleChatApprove(approvals, executions, sm))

		// Agent approval mode
		r.Get("/agent/mode", HandleGetApprovalMode(taskRunner))
		r.Post("/agent/mode", HandleSetApprovalMode(taskRunner))

		// Agent tasks
		r.Post("/tasks", HandleCreateTask(taskRunner))
		r.Get("/tasks/{id}", HandleGetTask(taskStore))
		r.Get("/sessions/{sessionID}/tasks", HandleListTasks(taskStore))
		r.Post("/tasks/{id}/start", HandleStartTask(taskRunner))
		r.Post("/tasks/{id}/stop", HandleStopTask(taskRunner))
		r.Post("/tasks/{id}/approve", HandleApproveTask(taskRunner, taskStore))
	})

	// WebSocket
	r.Get("/ws/terminal", HandleTerminalWSBridge(bridge))
	r.Get("/ws/events", HandleEventsWS(executions, taskStore, agg))

	return r
}
