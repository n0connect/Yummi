export type ChatProposal = {
  id?: string
  command?: string
  rationale?: string
  risk_level?: 'low' | 'medium' | 'high' | 'critical' | string
  requires_approval?: boolean
  target_tool?: string
}

export type ExecutionRecord = {
  id: string
  session_id: string
  proposal_id?: string
  status: 'queued' | 'running' | 'awaiting_egress_approval' | 'completed' | 'failed' | 'canceled' | string
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

export type PlanStepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | string

export type PlanStep = {
  id: string
  description: string
  status: PlanStepStatus
  result?: string
  error?: string
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
  question?: string
  last_error?: string
  pending_proposal_id?: string
  pending_command?: string
  pending_risk?: string
  created_at?: number
  updated_at?: number
}

// ─── TRACE / observability types ─────────────────────────────────────────────

export type ActivityKind =
  | 'plan' | 'command' | 'command_result' | 'memory_recalled' | 'skill_applied'
  | 'handoff_started' | 'handoff_completed' | 'finding' | 'context_saved' | 'ask' | 'finish'

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
  status: 'connected' | 'disconnected' | 'not_configured'
}

export type GatewayEgressMode = 'block' | 'allow' | 'ask'

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
