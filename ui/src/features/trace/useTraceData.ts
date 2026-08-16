import { useCallback, useEffect, useState } from 'react'
import type {
  ActivityEvent, Finding, PersistentContext, SkillInfo, Tactic, Target,
} from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { useChatStream } from '../chat/useChatStream'

// resolveSessionID reads the session the AGENT chat is bound to (same key the
// assistant persists), so TRACE shows the engagement the operator is running.
export function resolveSessionID(): string {
  try {
    return sessionStorage.getItem('yummi:chat:sessionId') ?? 'default'
  } catch {
    return 'default'
  }
}

export type TraceData = {
  activity: ActivityEvent[]
  findings: Finding[]
  targets: Target[]
  tactics: Tactic[]
  context: PersistentContext[]
  skills: SkillInfo[]
  roles: string[]
  refresh: () => void
}

// useTraceData backfills the world model from the read APIs and keeps the live
// activity trace updated from the agent.activity websocket stream.
export function useTraceData(sessionID: string): TraceData {
  const [activity, setActivity] = useState<ActivityEvent[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [targets, setTargets]   = useState<Target[]>([])
  const [tactics, setTactics]   = useState<Tactic[]>([])
  const [context, setContext]   = useState<PersistentContext[]>([])
  const [skills, setSkills]     = useState<SkillInfo[]>([])
  const [roles, setRoles]       = useState<string[]>([])

  const refresh = useCallback(() => {
    const sid = sessionID || 'default'
    fetchJSON<ActivityEvent[]>(`/api/v1/sessions/${sid}/activity`).then(d => setActivity(d ?? [])).catch(() => {})
    fetchJSON<Finding[]>(`/api/v1/sessions/${sid}/findings`).then(d => setFindings(d ?? [])).catch(() => {})
    fetchJSON<Target[]>(`/api/v1/sessions/${sid}/targets`).then(d => setTargets(d ?? [])).catch(() => {})
    fetchJSON<Tactic[]>(`/api/v1/tactics`).then(d => setTactics(d ?? [])).catch(() => {})
    fetchJSON<PersistentContext[]>(`/api/v1/sessions/${sid}/context`).then(d => setContext(d ?? [])).catch(() => {})
    fetchJSON<SkillInfo[]>(`/api/v1/skills`).then(d => setSkills(d ?? [])).catch(() => {})
    fetchJSON<string[]>(`/api/v1/agent/roles`).then(d => setRoles(d ?? [])).catch(() => {})
  }, [sessionID])

  useEffect(() => { refresh() }, [refresh])

  // Live: append streamed activity for this session; refetch tactics/context on
  // events that change the world model so those panels stay current.
  const onActivity = useCallback((ev: ActivityEvent) => {
    if (ev.session_id !== (sessionID || 'default')) return
    setActivity(prev => [...prev, ev])
    if (ev.kind === 'finding') {
      fetchJSON<Finding[]>(`/api/v1/sessions/${ev.session_id}/findings`).then(d => setFindings(d ?? [])).catch(() => {})
    }
    if (ev.kind === 'command_result') {
      fetchJSON<Tactic[]>(`/api/v1/tactics`).then(d => setTactics(d ?? [])).catch(() => {})
    }
    if (ev.kind === 'context_saved') {
      fetchJSON<PersistentContext[]>(`/api/v1/sessions/${ev.session_id}/context`).then(d => setContext(d ?? [])).catch(() => {})
    }
  }, [sessionID])

  useChatStream({ onActivity })

  return { activity, findings, targets, tactics, context, skills, roles, refresh }
}
