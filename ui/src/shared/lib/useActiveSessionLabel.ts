import { useEffect, useState } from 'react'
import { fetchJSON } from '../api/http'
import type { SessionSummary } from '../../entities/types'
import { t } from '../i18n/strings'

const STORAGE_SID = 'yummi:chat:sessionId'

// Reads the SAME session id ChatPage owns (sessionStorage-backed) and
// resolves it to a human-readable label — used by the Bash and Desktop
// pages so switching tabs doesn't feel like stepping into a disconnected
// world: "you were just working on X in Agent, and X is still what you're
// looking at here." "default" (the legacy ad-hoc bucket) has no backend
// record to fetch, so it resolves to a fixed label instead of a 404.
export function useActiveSessionLabel(): string {
  const [label, setLabel] = useState<string>('')

  useEffect(() => {
    const sid = sessionStorage.getItem(STORAGE_SID) ?? 'default'
    if (sid === 'default') {
      setLabel(t('common.defaultSessionLabel'))
      return
    }
    let cancelled = false
    fetchJSON<SessionSummary>(`/api/v1/sessions/${sid}`)
      .then((s) => {
        if (!cancelled) setLabel(s.name || sid)
      })
      .catch(() => {
        if (!cancelled) setLabel(sid)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return label
}
