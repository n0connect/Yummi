export type ChatProposal = {
  id?: string
  command?: string
  rationale?: string
  risk_level?: 'low' | 'medium' | 'high' | 'critical' | string
  requires_approval?: boolean
  target_tool?: string
}

// ChatStreamEvent mirrors the orchestrator's conductor.StreamEvent — a raw,
// incremental display token (or the terminal done signal) broadcast over
// /ws/events as a "chat.stream" event while a conductor.Engine.Chat /
// ChatWithHistory call is streaming from Ollama. Purely a display aid: the
// authoritative reply is still the normal /api/v1/chat REST response.
export type ChatStreamEvent = {
  session_id: string
  turn_id: string
  delta?: string
  done: boolean
}

export type ExecutionRecord = {
  id: string
  session_id: string
  proposal_id?: string
  status:
    | 'queued'
    | 'running'
    | 'awaiting_egress_approval'
    | 'awaiting_threat_approval'
    | 'completed'
    | 'failed'
    | 'canceled'
    | string
  command: string
  mode?: string
  output?: string
  error?: string
  // The host/IP the command targets, set only while status is
  // 'awaiting_egress_approval' (gateway Ask mode) so the UI can name what
  // external access is pending operator approval.
  egress_target?: string
  // Content-layer threat verdict for this command's output, set by the
  // orchestrator when the raw output trips known prompt-injection / C2 /
  // exfiltration patterns. Surfaced in the card AND prepended when the output
  // is fed back to the conductor, so the model treats it as untrusted DATA.
  security_banner?: string
  // ANSI/C1-escape-stripped copies of output/error. output/error stay raw for
  // operator display (escape sequences included, intentionally); anything
  // that re-feeds this content back into the LLM (analyzeOutput below) must
  // use these instead — an unstripped escape sequence could be used by a
  // hostile target to smuggle a payload past the banner scan.
  sanitized_output?: string
  sanitized_error?: string
  created_at?: number
  updated_at?: number
  // Set only on periodic "still running" heartbeat events while status is
  // 'running' — never on a terminal record. Lets the UI show live elapsed
  // time instead of a static "running" state for however long a real
  // command takes, so a slow scan doesn't look identical to a hang.
  elapsed_secs?: number
  // output_chunk/output_source mirror elapsed_secs's transient-field
  // discipline: set only on a WS "execution.output" event (see
  // orchestrator's execution.Store.publishOutputChunk) while status is
  // 'running', never on a terminal record. A raw fragment of stdout/stderr
  // as the sandboxed command produces it — purely a live-display aid, always
  // superseded by output/error once the execution finishes. See
  // AssistantPanel's liveOutputs for how these get accumulated and rendered.
  output_chunk?: string
  output_source?: 'stdout' | 'stderr' | string
  // The one-sentence "why this command, right now" the conductor's proposal
  // or the agent's own tool call carried alongside it — lets the operator
  // scan a long execution list by intent instead of decoding each raw
  // command line. Empty for an execution whose caller had none to give.
  rationale?: string
}

// ─── Session + persisted chat (from /api/v1/sessions[/{id}/messages]) ─────────

export type SessionSummary = {
  id: string
  name: string
  created_at?: number
  updated_at?: number
  status?: string
  gateway_level?: number
}

// One persisted conversation turn. payload is opaque JSON the server attaches
// to assistant turns (intent / proposals / task id); the UI currently renders
// reloaded turns as plain text, so payload is informational.
export type ServerChatMessage = {
  id: number
  session_id: string
  role: 'user' | 'assistant' | 'system' | string
  content: string
  payload?: string
  created_at?: number
}

// ─── Agent Task types (from /api/v1/tasks + /ws/events task.status) ───────────

export type TaskStatus =
  | 'planning'
  | 'planned'
  | 'running'
  | 'awaiting_approval'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'stopped'
  | 'interrupted'
  | string

export type PlanStepStatus =
  | 'pending'
  | 'running'
  | 'done'
  | 'failed'
  | 'skipped'
  | string

export type PlanStep = {
  id: string
  description: string
  status: PlanStepStatus
  result?: string
  error?: string
}

// FindingVerdict/OracleKind mirror the Go types in
// orchestrator/internal/agent/verdict.go — a verdict is computed server-side
// by checking the finding's cited evidence against the real recorded output
// of the plan step it names, never taken on the model's word.
export type FindingVerdict = 'candidate' | 'verified' | 'unsupported' | string
export type OracleKind = 'tool_evidence' | string

export type FindingRecord = {
  title: string
  verdict: FindingVerdict
  oracle_kind?: OracleKind
  evidence?: string
  step_id?: string
  reason?: string
}

export type AgentTask = {
  id: string
  session_id: string
  // Set when this task is a sub-agent run delegated via [HANDOFF] from
  // another task; empty for a top-level task. Lets the UI surface a
  // sub-task's own pending approval nested under the parent it watches.
  parent_task_id?: string
  goal: string
  status: TaskStatus
  plan?: PlanStep[]
  steps_used: number
  step_budget: number
  summary?: string
  findings?: string[]
  // finding_records is the structured, verdict-bearing counterpart to
  // findings — server-computed, never a bare model assertion (see
  // orchestrator/internal/agent/verdict.go). May be shorter than findings:
  // only findings the model attached evidence/step_id to produce a record.
  finding_records?: FindingRecord[]
  question?: string
  last_error?: string
  pending_proposal_id?: string
  pending_command?: string
  pending_risk?: string
  // Operator-set, per-task override (POST /api/v1/tasks/{id}/auto-approve):
  // when true, a proposal below high/critical risk skips the ask_permission
  // gate for the REST of this one task — high/critical and anything
  // destructiveFloor forces still always gate. Distinct from the runner-wide
  // approval mode, which only ever applies to tasks started after a change.
  auto_approve_non_critical?: boolean
  created_at?: number
  updated_at?: number
}

export type AgentCapabilities = {
  backend: 'graph' | 'eino' | string
  execution_model: string
  recovery_model: string
  resumable: boolean
  native_tool_calling: boolean
  approval_mode: 'ask_permission' | 'auto' | string
  roles: string[]
  max_handoff_depth: number
  max_concurrent_sub_agents: number
  memory_enabled: boolean
  retrieval_model: string
  skills_enabled: boolean
  skill_count: number
  skill_governance: string
  context_window: number
}

export type AgentDelegationRun = {
  id: string
  parent_task_id: string
  task_id?: string
  role: string
  status: 'running' | 'completed' | 'failed' | 'interrupted' | string
}

// ─── TRACE / observability types ─────────────────────────────────────────────

export type ActivityKind =
  | 'plan'
  | 'command'
  | 'command_result'
  | 'memory_recalled'
  | 'skill_applied'
  | 'skill_suggested'
  | 'handoff_started'
  | 'handoff_completed'
  | 'finding'
  | 'context_saved'
  | 'context_rejected'
  | 'ask'
  | 'finish'

// One legible step in the agent's work (from /ws/events agent.activity +
// /api/v1/sessions/{id}/activity). parent_task_id nests sub-agent events.
export type ActivityEvent = {
  id: string
  session_id: string
  task_id: string
  parent_task_id?: string
  kind: ActivityKind
  summary: string
  detail?: string
  timestamp: number
}

// An in-scope host/IP declared or discovered for a session (from
// /api/v1/sessions/{id}/targets). Lets the findings view resolve a
// Finding.target_id into the human-readable host/IP the operator recognizes.
export type Target = {
  id: string
  session_id: string
  value: string
  in_scope: boolean
  notes?: string
  created_at?: number
}

// ─── Attack Surface Management (ASM) & Asset Graph types ────────────────────

export type AssetType =
  | 'domain'
  | 'subdomain'
  | 'ip'
  | 'port'
  | 'endpoint'
  | 'parameter'
  | 'technology'
  | 'cloud_bucket'
  | 'secret'
  | string

export type AssetNode = {
  id: string
  session_id: string
  parent_id?: string
  type: AssetType
  value: string
  metadata?: Record<string, unknown>
  created_at: number
  updated_at?: number
}

export type AssetTree = AssetNode & {
  children?: AssetTree[]
}

export type ReportFormat = 'hackerone' | 'bugcrowd' | 'executive' | 'json'

export type ProxyStatus = {
  status: string
  mode: string
  ca_installed: boolean
  supported_protocols: string[]
  supported_sources: string[]
}

export type ScopeRuleKind = 'include' | 'exclude'

export type ScopeRule = {
  id: string
  session_id: string
  pattern: string
  kind: ScopeRuleKind
  note?: string
  created_at: number
}

export type ScopeVerdict = {
  in_scope: boolean
  reason: string
  matched_rule?: string
}

// A structured security finding (from /api/v1/sessions/{id}/findings).
export type Finding = {
  id: string
  session_id: string
  target_id?: string
  severity: 'info' | 'low' | 'medium' | 'high' | 'critical' | string
  title: string
  description?: string
  evidence?: string
  source?: 'manual' | 'agent' | 'subagent' | string
  task_id?: string
  created_at?: number
}

// A learned, target-scrubbed procedural tactic (from /api/v1/tactics).
export type Tactic = {
  id: string
  tool: string
  command_pattern: string
  outcome: 'worked' | 'failed' | 'inconclusive' | string
  context_tags?: string[]
  note?: string
  hit_count: number
  last_used_at: number
  created_at: number
}

// A persistent memory element — code/connection map (from /context).
export type PersistentContext = {
  id: string
  session_id: string
  key: string
  value: string
  created_at: number
  updated_at: number
}

// A declarative skill playbook (from /api/v1/skills).
export type SkillInfo = {
  name: string
  description?: string
  triggers?: string[]
  tools?: string[]
  knowledge?: string
}

// The operator briefing (from /api/v1/operator/briefing) — the same
// accumulated knowledge the AI conductor is fed, surfaced for the operator's
// own manual work: matching skills, tactics that worked before, and findings
// already on record for the session.
export type OperatorBriefing = {
  goal: string
  tool: string
  skills: SkillInfo[]
  tactics: Tactic[]
  findings: Finding[]
}

// A curator-drafted skill awaiting operator review (from
// /api/v1/skills/pending). Never auto-loaded — approval writes it to disk
// and hot-adds it to the live registry; rejection just marks the row.
export type PendingSkill = {
  id: string
  status: 'pending' | 'approved' | 'rejected' | string
  origin: string
  task_id: string
  session_id: string
  name: string
  draft_yaml: string
  trajectory: string
  threat_scan_result?: string
  reject_reason?: string
  created_at: number
  resolved_at?: number
}

// ─── Gateway types (from /api/v1/gateway/rules + /audit + gateway.status WS) ──

export type GatewayRule = {
  id: string
  action: 'allow' | 'block'
  network: string
  description: string
  expires_at: number | null
  source: 'config' | 'agent' | 'manual'
}

export type GatewayAuditRow = {
  id: number
  timestamp: number
  src_ip: string
  dst_ip: string
  decision: string
  reason: string
}

export type GatewayStatus = {
  level: number
  rules_active: boolean
  uptime_secs?: number
  policy_kernel_consistent: boolean
  enforcement_generation: number
  last_reconcile_error?: string
  status: 'connected' | 'disconnected' | 'not_configured'
}

export type GatewayEgressMode =
  | 'block_all'
  | 'block_except_rules'
  | 'allow_all'
  | 'ask'
  | 'block'
  | 'allow'

export type GatewayEgressModeResponse = {
  mode: GatewayEgressMode
  status: 'connected' | 'unreachable' | 'not_configured'
}

// ─── System Status types (from /api/v1/system/status) ────────────────────────

export type ModuleStatus = {
  name: string
  status: 'healthy' | 'degraded' | 'down' | 'unknown' | 'not_connected' | string
  details?: string
  uptime_secs?: number
  meta?: Record<string, unknown>
}

export type SystemStatusResponse = {
  overall: 'healthy' | 'degraded' | 'down' | string
  modules: ModuleStatus[]
  checked_at: string
}

// ─── Log Entry type (from /api/v1/system/logs SSE) ──────────────────────────

export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR' | string

export type LogEntry = {
  t: string
  level: LogLevel
  msg: string
  src?: string
  attrs?: Record<string, unknown>
}
