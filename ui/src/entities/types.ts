export type InfraState = {
  sandboxStatus: string
  gatewayStatus: string
  gatewayLevel: string
}

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
  status: 'queued' | 'running' | 'completed' | 'failed' | 'canceled' | string
  command: string
  mode?: string
  output?: string
  error?: string
  created_at?: number
  updated_at?: number
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
