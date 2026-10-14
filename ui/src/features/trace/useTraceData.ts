import { useCallback, useEffect, useRef, useState } from 'react'
import type {
  ActivityEvent, AgentCapabilities, AssetTree, Finding, PersistentContext, SkillInfo, Tactic, Target,
} from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { useChatStream } from '../chat/useChatStream'
import { t } from '../../shared/i18n/strings'

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
  assets: AssetTree[]
  tactics: Tactic[]
  context: PersistentContext[]
  skills: SkillInfo[]
  roles: string[]
  capabilities: AgentCapabilities | null
  loading: boolean
  errors: string[]
  refresh: () => void
}

// useTraceData backfills the world model from the read APIs and keeps the live
// activity trace updated from the agent.activity websocket stream.
export function useTraceData(sessionID: string): TraceData {
  const [activity, setActivity] = useState<ActivityEvent[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [targets, setTargets]   = useState<Target[]>([])
  const [assets, setAssets]     = useState<AssetTree[]>([])
  const [tactics, setTactics]   = useState<Tactic[]>([])
  const [context, setContext]   = useState<PersistentContext[]>([])
  const [skills, setSkills]     = useState<SkillInfo[]>([])
  const [roles, setRoles]       = useState<string[]>([])
  const [capabilities, setCapabilities] = useState<AgentCapabilities | null>(null)
  const [loading, setLoading] = useState(true)
  const [errors, setErrors] = useState<string[]>([])
  const version = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const requests = useRef<Record<string, number>>({})
  const labels: Record<string, string> = {
    activity: t('trace.sectionActivity'), findings: t('trace.sectionFindings'),
    targets: t('trace.sectionAssets'), assets: t('trace.sectionAssets'),
    tactics: t('trace.sectionTactics'), context: t('trace.sectionContext'),
    skills: t('trace.sectionSkills'), roles: t('trace.sectionRoles'), capabilities: t('trace.sectionCapabilities')
  }
  const load = useCallback(async <T,>(key: string, url: string, setter: (value: T) => void, fallback: T, generation: number, signal?: AbortSignal) => {
    const request = (requests.current[key] ?? 0) + 1
    requests.current[key] = request
    try {
      const data = await fetchJSON<T>(url, { signal })
      if (data === undefined) throw new Error('Invalid response')
      if (generation !== version.current || request !== requests.current[key] || signal?.aborted) return
      setter(data ?? fallback)
      setErrors(previous => previous.filter(item => item !== key))
    } catch {
      if (generation !== version.current || request !== requests.current[key] || signal?.aborted) return
      setErrors(previous => previous.includes(key) ? previous : [...previous, key])
    }
  }, [])

  const refresh = useCallback(() => {
    const sid = sessionID || 'default'
    controller.current?.abort()
    controller.current = new AbortController()
    const signal = controller.current.signal
    const generation = ++version.current
    setLoading(true)
    setErrors([])
    void Promise.all([
      load('activity', `/api/v1/sessions/${sid}/activity`, setActivity, [], generation, signal),
      load('findings', `/api/v1/sessions/${sid}/findings`, setFindings, [], generation, signal),
      load('targets', `/api/v1/sessions/${sid}/targets`, setTargets, [], generation, signal),
      load('assets', `/api/v1/sessions/${sid}/assets?format=tree`, setAssets, [], generation, signal),
      load('tactics', '/api/v1/tactics', setTactics, [], generation, signal),
      load('context', `/api/v1/sessions/${sid}/context`, setContext, [], generation, signal),
      load('skills', '/api/v1/skills', setSkills, [], generation, signal),
      load('roles', '/api/v1/agent/roles', setRoles, [], generation, signal),
      load('capabilities', '/api/v1/agent/capabilities', setCapabilities, null, generation, signal)
    ]).finally(() => { if (generation === version.current && !signal.aborted) setLoading(false) })
  }, [sessionID, load])

  useEffect(() => {
    refresh()
    return () => { version.current++; controller.current?.abort() }
  }, [refresh])

  // Live: append streamed activity for this session; refetch tactics/context on
  // events that change the world model so those panels stay current.
  const onActivity = useCallback((ev: ActivityEvent) => {
    if (ev.session_id !== (sessionID || 'default')) return
    setActivity(prev => prev.some(item => item.id === ev.id) ? prev : [...prev, ev].slice(-1000))
    if (ev.kind === 'finding') {
      void load('findings', `/api/v1/sessions/${ev.session_id}/findings`, setFindings, [], version.current, controller.current?.signal)
    }
    if (ev.kind === 'command_result') {
      void load('tactics', '/api/v1/tactics', setTactics, [], version.current, controller.current?.signal)
      void load('assets', `/api/v1/sessions/${ev.session_id}/assets?format=tree`, setAssets, [], version.current, controller.current?.signal)
    }
    if (ev.kind === 'context_saved') {
      void load('context', `/api/v1/sessions/${ev.session_id}/context`, setContext, [], version.current, controller.current?.signal)
    }
  }, [sessionID, load])

  // Ordered replay covers ordinary reconnects. refresh() is reserved for the
  // explicit resync signal emitted when the bounded server window was lost.
  useChatStream({ sessionID: sessionID || 'default', onActivity, onReconnect: refresh })

  return { activity, findings, targets, assets, tactics, context, skills, roles, capabilities, refresh, loading, errors: [...new Set(errors.map(key => labels[key] ?? key))] }
}
