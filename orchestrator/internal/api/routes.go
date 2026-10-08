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
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// NewRouter creates the Chi router with all routes.
func NewRouter(sm *session.Manager, ks *knowledge.Store, skillReg *skill.Registry, cfg *config.Config, assistant *conductor.Engine, approvals *conductor.ApprovalStore, executions *execution.Store, bridge *sandbox.Bridge, pc *gateway.Client, lb *LogBuffer, llmClient *llm.Client, resilientLLM *llm.Resilient, agg *health.Aggregator, taskStore *agent.Store, taskRunner agent.TaskRunner, curatorStore *skillcurator.Store, curatorWorker *skillcurator.Worker, agentTerm agentTerminalCaller, mcpSchemaHints map[string]string) http.Handler {
	r := chi.NewRouter()

	// Middleware
	// Note: chi's middleware.RealIP is deliberately not used — it's deprecated
	// as of chi v5.3.0 (GHSA-3fxj-6jh8-hvhx et al., IP spoofing via blind trust
	// of client-supplied XFF/X-Real-IP/True-Client-IP headers) and nothing in
	// this codebase ever reads the RemoteAddr it would rewrite.
	r.Use(middleware.RequestID)
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

		// Assets (ASM & Asset Matrix)
		r.Get("/sessions/{sessionID}/assets", HandleListAssets(ks))
		r.Post("/sessions/{sessionID}/assets", HandleCreateAsset(ks))

		// Program scope (bug-bounty program scope bookkeeping — distinct from
		// the gateway's own network-egress scope; never gates execution)
		r.Get("/sessions/{sessionID}/scope-rules", HandleListScopeRules(ks))
		r.Post("/sessions/{sessionID}/scope-rules", HandleCreateScopeRules(ks))
		r.Delete("/sessions/{sessionID}/scope-rules/{ruleID}", HandleDeleteScopeRule(ks))
		r.Post("/sessions/{sessionID}/scope-rules/classify", HandleClassifyScope(ks))

		// Reports (Multi-format Pentest & Bug Bounty Report Generator)
		r.Get("/sessions/{sessionID}/report", HandleGenerateReport(ks, sm))

		// TRACE observability — read-only views of the agent's world model.
		r.Get("/sessions/{sessionID}/activity", HandleListActivity(taskStore))
		r.Get("/sessions/{sessionID}/context", HandleListContext(ks))
		r.Delete("/sessions/{sessionID}/context/{key}", HandleDeleteContext(ks))
		r.Get("/tactics", HandleListTactics(ks))
		r.Get("/skills", HandleListSkills(skillReg))
		r.Get("/skills/pending", HandleListPendingSkills(curatorStore))
		r.Post("/skills/pending/{id}/approve", HandleApprovePendingSkill(curatorStore, skillReg, autoSkillDir(cfg)))
		r.Post("/skills/pending/{id}/reject", HandleRejectPendingSkill(curatorStore))
		r.Post("/skills/propose", HandleProposeSkillTrajectory(curatorWorker))
		// Operator briefing — "operator drives, AI assists": the same
		// accumulated knowledge the agent is fed (matching skills, tactics,
		// findings on record), surfaced for the operator's own manual work.
		r.Get("/operator/briefing", HandleOperatorBriefing(skillReg, ks))
		r.Get("/agent/roles", HandleListRoles(taskRunner))
		r.Get("/agent/capabilities", HandleAgentCapabilities(taskRunner))
		r.Get("/agent/delegations", HandleListDelegations(taskRunner))
		r.Post("/agent/delegations/{id}/interrupt", HandleInterruptDelegation(taskRunner))
		r.Get("/sessions/{sessionID}/audit", HandleListAudit(sm))
		r.Get("/sessions/{sessionID}/executions", HandleListExecutions(executions))
		r.Get("/executions/{id}", HandleGetExecution(executions))
		r.Post("/executions/{id}/cancel", HandleCancelExecution(executions, sm))
		r.Post("/executions/{id}/resolve-egress", HandleResolveEgress(executions, pc, sm))
		r.Post("/executions/{id}/resolve-threat", HandleResolveThreatApproval(executions, sm))

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
		r.Get("/gateway/hostname-policy", HandleGatewayHostnamePolicy(pc))
		r.Post("/gateway/hostname-policy", HandleGatewayAddHostnameRule(pc))
		r.Delete("/gateway/hostname-policy", HandleGatewayRemoveHostnameRule(pc))

		// sandbox status & direct exec
		r.Get("/sandbox/status", HandleSandboxStatus(bridge))
		r.Post("/sandbox/exec", HandleSandboxExec(bridge, pc, sm))

		// Proxy & traffic ingestion bridge
		r.Get("/proxy/status", HandleProxyStatus())
		r.Post("/proxy/inject", HandleProxyInject(ks, sm))

		// System aggregated status, metrics & debug log stream
		r.Get("/system/status", HandleSystemStatus(bridge, pc, cfg.DockerSocket, assistant))
		r.Get("/system/health-metrics", HandleHealthMetrics(agg))
		r.Get("/system/logs", HandleSystemLogs(lb))
		r.Get("/llm/status", HandleLLMStatus(llmClient, resilientLLM))

		// Chat
		r.Post("/chat", HandleChat(assistant, approvals, sm, taskRunner, taskStore, cfg.ChatTimeout()))
		r.Post("/chat/approve", HandleChatApprove(approvals, executions, sm, mcpSchemaHints))

		// Agent approval mode
		r.Get("/agent/mode", HandleGetApprovalMode(taskRunner))
		r.Post("/agent/mode", HandleSetApprovalMode(taskRunner))

		// Agent-owned terminals: read-only watch view (control stays with the agent)
		r.Get("/agent-terminals", HandleListAgentTerminals(agentTerm))
		r.Get("/agent-terminals/{id}", HandleReadAgentTerminal(agentTerm))

		// Agent tasks
		r.Post("/tasks", HandleCreateTask(taskRunner))
		r.Get("/tasks/{id}", HandleGetTask(taskStore))
		r.Get("/sessions/{sessionID}/tasks", HandleListTasks(taskStore))
		r.Post("/tasks/{id}/start", HandleStartTask(taskRunner))
		r.Post("/tasks/{id}/stop", HandleStopTask(taskRunner))
		r.Post("/tasks/{id}/approve", HandleApproveTask(taskRunner, taskStore))
		r.Post("/tasks/{id}/auto-approve", HandleSetTaskAutoApprove(taskRunner))
	})

	// WebSocket
	r.Get("/ws/terminal", HandleTerminalWSBridge(bridge))
	r.Get("/ws/desktop", HandleDesktopWSBridge(cfg.SandboxDesktopAddr))
	r.Get("/ws/events", HandleEventsWS(executions, taskStore, agg, curatorStore, assistant))

	return r
}

// autoSkillDir is where an approved curator-drafted skill is written — the
// same skills/ directory skill.LoadDir already reads at startup, so an
// approval this session is picked up on the next restart even if the
// in-process registry Add() were ever bypassed.
func autoSkillDir(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.AutoSkillDir
}
