import { useCallback, useState } from 'react'
import type { OperatorBriefing } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'

export type BriefingState = {
  data: OperatorBriefing | null
  loading: boolean
  error: string | null
  // Queried once so the panel can distinguish "haven't asked yet" (show the
  // hint) from "asked, nothing matched" (show the empty state).
  queried: boolean
  query: (params: { goal?: string; tool?: string; session?: string }) => Promise<void>
  clear: () => void
}

// useOperatorBriefing drives the "operator drives, AI assists" surface: an
// on-demand pull of Yummi's accumulated knowledge (matching skills, tactics
// that worked before, findings on record) for the operator's OWN manual work,
// via GET /api/v1/operator/briefing. Deliberately NOT live-pushed like the
// skills/gateway hooks — a briefing is a question the operator asks when they
// want it ("what do you know about nmap?"), not a stream that updates on its
// own. Nothing here changes server state; it only reads what already exists.
export function useOperatorBriefing(): BriefingState {
  const [data, setData] = useState<OperatorBriefing | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [queried, setQueried] = useState(false)

  const query = useCallback(async ({ goal, tool, session }: { goal?: string; tool?: string; session?: string }) => {
    const params = new URLSearchParams()
    if (goal) params.set('goal', goal)
    if (tool) params.set('tool', tool)
    if (session && session !== 'default') params.set('session', session)
    setLoading(true)
    setError(null)
    try {
      const result = await fetchJSON<OperatorBriefing>(`/api/v1/operator/briefing?${params.toString()}`)
      setData(result)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setData(null)
    } finally {
      setLoading(false)
      setQueried(true)
    }
  }, [])

  const clear = useCallback(() => {
    setData(null)
    setError(null)
    setQueried(false)
  }, [])

  return { data, loading, error, queried, query, clear }
}
