import { useCallback, useEffect, useState } from 'react'
import type { PendingSkill } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'

const POLL_MS = 5_000

export type PendingSkillsData = {
  pending: PendingSkill[]
  refresh: () => void
  approve: (id: string, reason?: string) => Promise<void>
  reject: (id: string, reason?: string) => Promise<void>
}

// usePendingSkills polls curator-drafted skills awaiting operator review.
// Every draft here already passed threatscan once (Curator.ProposeFromTask);
// approve() re-scans server-side as defense-in-depth before it ever becomes
// a loadable skill.
export function usePendingSkills(): PendingSkillsData {
  const [pending, setPending] = useState<PendingSkill[]>([])

  const refresh = useCallback(() => {
    fetchJSON<PendingSkill[]>('/api/v1/skills/pending')
      .then(d => setPending(d ?? []))
      .catch(() => setPending([]))
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, POLL_MS)
    return () => clearInterval(id)
  }, [refresh])

  const approve = useCallback(async (id: string, reason = '') => {
    await fetchJSON(`/api/v1/skills/pending/${encodeURIComponent(id)}/approve`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason }),
    })
    refresh()
  }, [refresh])

  const reject = useCallback(async (id: string, reason = '') => {
    await fetchJSON(`/api/v1/skills/pending/${encodeURIComponent(id)}/reject`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason }),
    })
    refresh()
  }, [refresh])

  return { pending, refresh, approve, reject }
}
