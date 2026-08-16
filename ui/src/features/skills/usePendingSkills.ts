import { useCallback, useEffect, useState } from 'react'
import type { PendingSkill } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { useChatStream } from '../chat/useChatStream'

export type PendingSkillsData = {
  pending: PendingSkill[]
  // Recently rejected drafts (threat-scan-flagged or operator-rejected) —
  // read-only audit trail, see threat_scan_result/reject_reason on each row.
  rejected: PendingSkill[]
  refresh: () => void
  approve: (id: string, reason?: string) => Promise<void>
  reject: (id: string, reason?: string) => Promise<void>
}

// usePendingSkills tracks curator-drafted skills awaiting (or past) operator
// review. Updates live over the shared /ws/events socket's skill.status
// events (fired on every create/approve/reject/activation-rollback in
// orchestrator/internal/skillcurator.Store) rather than polling — a fresh
// GET only happens on mount and in direct response to a push, or after this
// tab's own approve()/reject() call. Every draft here already passed
// threatscan once (Curator.ProposeFromTask); approve() re-scans server-side
// as defense-in-depth before it ever becomes a loadable skill.
export function usePendingSkills(): PendingSkillsData {
  const [pending, setPending] = useState<PendingSkill[]>([])
  const [rejected, setRejected] = useState<PendingSkill[]>([])

  const refresh = useCallback(() => {
    fetchJSON<PendingSkill[]>('/api/v1/skills/pending')
      .then(d => setPending(d ?? []))
      .catch(() => setPending([]))
    fetchJSON<PendingSkill[]>('/api/v1/skills/pending?status=rejected')
      .then(d => setRejected(d ?? []))
      .catch(() => setRejected([]))
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // Any skill.status push (created / approved / rejected / rolled back to
  // pending after a failed activation) invalidates both lists — cheap and
  // simple at this data volume, and avoids reasoning about partial merges
  // for a store whose rows move between exactly two fetched views.
  const onSkillEvent = useCallback(() => { refresh() }, [refresh])
  useChatStream({ onSkillEvent })

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

  return { pending, rejected, refresh, approve, reject }
}
