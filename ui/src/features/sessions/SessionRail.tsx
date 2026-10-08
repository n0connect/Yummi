import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import type { SessionSummary, ServerChatMessage, AgentTask, ExecutionRecord, Finding } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'
import './SessionRail.css'

function ConversationIcon({ open }: { open: boolean }): JSX.Element {
  return (
    <svg
      className="rail-toggle-icon"
      viewBox="0 0 20 20"
      fill="none"
      aria-hidden="true"
    >
      {open ? (
        <path d="m12 5-5 5 5 5" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
      ) : (
        <>
          <rect x="2.75" y="3.25" width="14.5" height="13.5" rx="3" stroke="currentColor" strokeWidth="1.5" />
          <path d="M7 7.25h6M7 10.25h4M6.25 16.75l-2.5 1v-3.1" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </>
      )}
    </svg>
  )
}

function PinIcon({ pinned }: { pinned: boolean }): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill={pinned ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3.5 11.5h9M8 11.5v3.5M5 11.5V7.5L4 6V4h8v2l-1 1.5v4" />
    </svg>
  )
}

function ExportIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M2.5 10.5v2.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1v-2.5M5 6.5l3 3 3-3M8 2v7.5" />
    </svg>
  )
}

function ArchiveIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="2.5" width="12" height="3" rx="0.5" />
      <path d="M3.5 5.5v7.5a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V5.5M6.5 8.5h3" />
    </svg>
  )
}

function UnarchiveIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="2.5" width="12" height="3" rx="0.5" />
      <path d="M3.5 5.5v7.5a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V5.5M8 11.5V7M5.5 9l2.5-2.5 2.5 2.5" />
    </svg>
  )
}

function TrashIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M2.5 4h11M5.5 4V2.5a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1V4M12.5 4v9a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1V4M6.5 7v4M9.5 7v4" />
    </svg>
  )
}

function ConfirmCheckIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3.5 8.5l3 3 6-6" />
    </svg>
  )
}

type Props = {
  open: boolean
  onToggle: () => void
  activeSessionID: string
  // refreshKey bumps when AssistantPanel creates/renames a session, so the
  // list reflects a brand-new conversation and its auto-title without polling.
  refreshKey: number
  onSelect: (sessionID: string) => void
  onNewChat?: () => void
}

function shortWhen(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  return sameDay
    ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString([], { day: '2-digit', month: '2-digit' })
}

/**
 * A collapsible left rail of past conversations. Default-closed so it never
 * eats the chat column; an icon button slides it in. Selecting a session tells
 * the parent to switch the active conversation (AssistantPanel then rehydrates
 * that session's messages). Mirrors the Claude-style session switcher.
 */
export function SessionRail({ open, onToggle, activeSessionID, refreshKey, onSelect, onNewChat }: Props): JSX.Element {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(false)
  const [query, setQuery] = useState('')
  const [showArchived, setShowArchived] = useState(false)
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)
  const confirmTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [pinnedSessionIds, setPinnedSessionIds] = useState<string[]>(() => {
    try {
      const raw = localStorage.getItem('yummi_pinned_sessions')
      return raw ? JSON.parse(raw) : []
    } catch {
      return []
    }
  })

  useEffect(() => {
    return () => {
      if (confirmTimerRef.current) clearTimeout(confirmTimerRef.current)
    }
  }, [])

  const load = useCallback(() => {
    const controller = new AbortController()
    setLoading(true)
    fetchJSON<SessionSummary[]>('/api/v1/sessions', { signal: controller.signal })
      .then(rows => {
        setSessions(rows ?? [])
        setError(false)
      })
      .catch(err => { if (err?.name !== 'AbortError') setError(true) })
      .finally(() => setLoading(false))
    return () => controller.abort()
  }, [])

  // Reload whenever the rail opens or a session is created/renamed elsewhere.
  // load() returns an abort function so a stale in-flight request from a
  // rapid open/close/open never overwrites fresher data.
  useEffect(() => {
    if (!open) return
    return load()
  }, [open, refreshKey, load])

  // Escape closes the rail, matching AssistantPanel's proposal-bar behavior.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onToggle() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onToggle])

  const handleTogglePin = useCallback((id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    setPinnedSessionIds(prev => {
      const isPinned = prev.includes(id)
      const next = isPinned ? prev.filter(pId => pId !== id) : [id, ...prev]
      try {
        localStorage.setItem('yummi_pinned_sessions', JSON.stringify(next))
      } catch {
        // ignore storage errors
      }
      return next
    })
  }, [])

  const handleExport = useCallback(async (s: SessionSummary, e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      // Plain chat_messages rows are only a fraction of what actually
      // happened in a session — every agent task (goal/plan/summary),
      // every executed command, and every recorded finding lives in its
      // own separate table/endpoint and is reconstructed client-side at
      // view time (AssistantPanel's onTask/onExecution handlers), never
      // persisted back as a chat_messages row. Exporting messages alone
      // silently dropped the entire substance of any session with real
      // agent activity — the commands run, their output, the task's own
      // plan/summary — leaving only whatever plain chat bubbles existed
      // around it. Fetching all four sources and merging them by
      // timestamp reconstructs the same complete picture the live UI
      // shows, not just its chat sidebar. Each fetch is independently
      // tolerant of failure (an endpoint erroring or returning nothing)
      // so one missing data source never blanks the whole export.
      const [msgs, tasks, executions, findings] = await Promise.all([
        fetchJSON<ServerChatMessage[]>(`/api/v1/sessions/${s.id}/messages?limit=1000`).catch(() => []),
        fetchJSON<AgentTask[]>(`/api/v1/sessions/${s.id}/tasks`).catch(() => []),
        fetchJSON<ExecutionRecord[]>(`/api/v1/sessions/${s.id}/executions?limit=1000`).catch(() => []),
        fetchJSON<Finding[]>(`/api/v1/sessions/${s.id}/findings`).catch(() => [])
      ])

      type ExportItem = { at: number; render: () => string[] }
      const items: ExportItem[] = []

      for (const m of msgs ?? []) {
        items.push({
          at: m.created_at ?? 0,
          render: () => [`### [${m.role.toUpperCase()}]`, '', m.content]
        })
      }
      for (const t of tasks ?? []) {
        items.push({
          at: t.created_at ?? 0,
          render: () => {
            const out = [`### [AGENT TASK] ${t.status.toUpperCase()}`, '', `**Goal:** ${t.goal}`]
            if (t.plan && t.plan.length > 0) {
              out.push('', '**Plan:**')
              for (const step of t.plan) out.push(`- [${step.status}] ${step.description}`)
            }
            if (t.summary) out.push('', '**Summary:**', t.summary)
            if (t.findings && t.findings.length > 0) {
              out.push('', '**Findings:**')
              for (const f of t.findings) out.push(`- ${f}`)
            }
            out.push('', `_${t.steps_used}/${t.step_budget} steps used_`)
            return out
          }
        })
      }
      for (const ex of executions ?? []) {
        items.push({
          at: ex.created_at ?? 0,
          render: () => {
            const out = [`### [COMMAND] ${ex.status.toUpperCase()}`, '', '```', ex.command, '```']
            const output = ex.sanitized_output ?? ex.output
            if (output) out.push('', 'Output:', '```', output, '```')
            const error = ex.sanitized_error ?? ex.error
            if (error) out.push('', `Error: ${error}`)
            return out
          }
        })
      }
      for (const f of findings ?? []) {
        items.push({
          at: f.created_at ?? 0,
          render: () => {
            const out = [`### [FINDING] ${f.severity.toUpperCase()}: ${f.title}`]
            if (f.description) out.push('', f.description)
            if (f.evidence) out.push('', 'Evidence:', '```', f.evidence, '```')
            return out
          }
        })
      }
      items.sort((a, b) => a.at - b.at)

      const lines = [
        `# Yummi Pentest Session: ${s.name}`,
        `Session ID: ${s.id}`,
        `Exported at: ${new Date().toISOString()}`,
        '',
        '---',
        ''
      ]
      if (items.length > 0) {
        for (const item of items) {
          const time = item.at ? new Date(item.at * 1000).toLocaleString() : ''
          const rendered = item.render()
          lines.push(`${rendered[0]} ${time}`, ...rendered.slice(1))
          lines.push('', '---', '')
        }
      } else {
        lines.push('*(Nothing recorded in this session)*')
      }
      const blob = new Blob([lines.join('\n')], { type: 'text/markdown;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      const safeName = s.name.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 30) || 'session'
      a.download = `yummi-${safeName}-${s.id.slice(0, 8)}.md`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    } catch (err) {
      console.error('Failed to export session', err)
    }
  }, [])

  const archivedCount = useMemo(() => sessions.filter(s => s.status === 'closed').length, [sessions])

  const handleArchive = useCallback(async (id: string, currentStatus: string | undefined, e: React.MouseEvent) => {
    e.stopPropagation()
    const nextStatus = currentStatus === 'closed' ? 'active' : 'closed'
    try {
      await fetchJSON(`/api/v1/sessions/${id}`, {
        method: 'PATCH',
        body: JSON.stringify({ status: nextStatus })
      })
      setSessions(prev => prev.map(s => s.id === id ? { ...s, status: nextStatus } : s))
      if (nextStatus === 'closed' && id === activeSessionID) {
        onNewChat?.()
      }
    } catch (err) {
      console.error('Failed to update session archive status', err)
    }
  }, [activeSessionID, onNewChat])

  const handleDelete = useCallback(async (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    if (confirmDeleteId !== id) {
      setConfirmDeleteId(id)
      if (confirmTimerRef.current) clearTimeout(confirmTimerRef.current)
      confirmTimerRef.current = setTimeout(() => {
        setConfirmDeleteId(null)
      }, 3500)
      return
    }
    if (confirmTimerRef.current) clearTimeout(confirmTimerRef.current)
    setConfirmDeleteId(null)
    try {
      await fetchJSON(`/api/v1/sessions/${id}`, { method: 'DELETE' })
      setSessions(prev => prev.filter(s => s.id !== id))
      setPinnedSessionIds(prev => {
        const next = prev.filter(pId => pId !== id)
        try {
          localStorage.setItem('yummi_pinned_sessions', JSON.stringify(next))
        } catch {
          // ignore storage errors
        }
        return next
      })
      if (id === activeSessionID) {
        onNewChat?.()
      }
    } catch (err) {
      console.error('Failed to delete session', err)
    }
  }, [confirmDeleteId, activeSessionID, onNewChat])

  const filteredSessions = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase('tr-TR')
    const pool = sessions.filter(s => showArchived ? s.status === 'closed' : s.status !== 'closed')
    const list = needle
      ? pool.filter(session => session.name.toLocaleLowerCase('tr-TR').includes(needle))
      : pool
    return [...list].sort((a, b) => {
      const aPinned = pinnedSessionIds.includes(a.id)
      const bPinned = pinnedSessionIds.includes(b.id)
      if (aPinned && !bPinned) return -1
      if (!aPinned && bPinned) return 1
      return (b.updated_at ?? 0) - (a.updated_at ?? 0)
    })
  }, [query, sessions, showArchived, pinnedSessionIds])

  return (
    <>
      <button
        className={`rail-toggle ${open ? 'open' : ''}`}
        onClick={onToggle}
        title={open ? t('sessionRail.toggleOpenTitle') : t('sessionRail.toggleClosedTitle')}
        aria-label={t('sessionRail.toggleClosedTitle')}
        aria-expanded={open}
      >
        <ConversationIcon open={open} />
        <span className="rail-toggle-label">{t('sessionRail.toggleLabel')}</span>
      </button>

      {open && (
        <button
          type="button"
          className="session-rail-backdrop"
          onClick={onToggle}
          aria-label={t('sessionRail.backdropClose')}
          tabIndex={-1}
        />
      )}
      <aside className={`session-rail ${open ? 'open' : ''}`} role="dialog" aria-label={t('sessionRail.panelAriaLabel')} aria-modal="false">
        <div className="rail-header">
          <div className="rail-brand">
            <div className="rail-brand-text">
              <span className="rail-eyebrow">{t('sessionRail.eyebrow')}</span>
              <span className="rail-title">{t('sessionRail.title')}</span>
            </div>
          </div>
          <button className="rail-create" type="button" onClick={onNewChat} title={t('sessionRail.newButtonTitle')}>
            <span aria-hidden="true">＋</span>
            {t('sessionRail.newButtonLabel')}
          </button>
        </div>
        <div className="rail-search-section">
          <button
            type="button"
            className={`rail-archive-btn ${showArchived ? 'active' : ''}`}
            onClick={() => setShowArchived(prev => !prev)}
            title={showArchived ? t('sessionRail.activeButton') : t('sessionRail.archiveButton')}
            aria-pressed={showArchived}
          >
            <span className="rail-archive-btn-left">
              <ArchiveIcon />
              <span className="rail-archive-btn-text">
                {showArchived ? t('sessionRail.activeButton') : t('sessionRail.archiveButton')}
              </span>
            </span>
            <span className="rail-archive-count">
              {showArchived ? t('sessionRail.activeBadge') : archivedCount}
            </span>
          </button>
          <div className="rail-search-wrap">
            <svg className="rail-search-icon" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <circle cx="6.5" cy="6.5" r="4.75" stroke="currentColor" strokeWidth="1.5" />
              <path d="M10 10l4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
            <input
              type="text"
              className="rail-search"
              value={query}
              onChange={event => setQuery(event.target.value)}
              placeholder={showArchived ? t('sessionRail.searchArchived') : t('sessionRail.searchPlaceholder')}
              aria-label={showArchived ? t('sessionRail.searchArchived') : t('sessionRail.searchPlaceholder')}
            />
          </div>
        </div>
        <div className="rail-list">
          {loading && sessions.length === 0 && !error && (
            <div className="rail-empty">{t('sessionRail.loading')}</div>
          )}
          {error && (
            <div className="rail-empty rail-error">
              <span>{t('sessionRail.loadError')}</span>
              <button className="rail-retry" onClick={() => load()}>{t('common.retry')}</button>
            </div>
          )}
          {!error && !loading && sessions.length === 0 && (
            <div className="rail-empty rail-empty-intro">
              <span className="rail-empty-icon">◌</span>
              <strong>{t('sessionRail.emptyTitle')}</strong>
              <span>{t('sessionRail.emptyHint')}</span>
            </div>
          )}
          {!error && !loading && sessions.length > 0 && showArchived && filteredSessions.length === 0 && !query && (
            <div className="rail-empty rail-empty-intro">
              <span className="rail-empty-icon">◌</span>
              <strong>{t('sessionRail.emptyArchived')}</strong>
            </div>
          )}
          {!error && !loading && sessions.length > 0 && filteredSessions.length === 0 && (query || !showArchived) && (
            <div className="rail-empty">{t('sessionRail.noSearchResults')}</div>
          )}
          {filteredSessions.map(s => {
            const isPinned = pinnedSessionIds.includes(s.id)
            const isConfirming = confirmDeleteId === s.id
            return (
              <div
                key={s.id}
                className={`rail-item ${s.id === activeSessionID ? 'active' : ''} ${isPinned ? 'pinned' : ''}`}
                onClick={() => onSelect(s.id)}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(s.id) } }}
                title={s.name}
                aria-current={s.id === activeSessionID ? 'true' : undefined}
              >
                <span className="rail-item-main">
                  <span className="rail-item-name">{s.name}</span>
                  <span className="rail-item-meta">
                    <span className={`rail-item-dot ${s.id === activeSessionID ? 'active' : ''}`} />
                    {s.id === activeSessionID ? t('sessionRail.activeSession') : t('sessionRail.recentActivity')}
                  </span>
                </span>
                <div className="rail-item-side">
                  <div className="rail-item-info">
                    {isPinned && (
                      <svg className="rail-pin-badge" viewBox="0 0 16 16" aria-hidden="true">
                        <path d="M3.5 11.5h9M8 11.5v3.5M5 11.5V7.5L4 6V4h8v2l-1 1.5v4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" fill="none" />
                      </svg>
                    )}
                    <span className="rail-item-when">{shortWhen(s.updated_at)}</span>
                  </div>
                  <div className="rail-item-actions" onClick={e => e.stopPropagation()}>
                    <button
                      type="button"
                      className={`rail-action-btn ${isPinned ? 'active' : ''}`}
                      onClick={(e) => handleTogglePin(s.id, e)}
                      title={isPinned ? t('sessionRail.unpin') : t('sessionRail.pin')}
                      aria-label={isPinned ? t('sessionRail.unpin') : t('sessionRail.pin')}
                    >
                      <PinIcon pinned={isPinned} />
                    </button>
                    <button
                      type="button"
                      className="rail-action-btn"
                      onClick={(e) => void handleExport(s, e)}
                      title={t('sessionRail.export')}
                      aria-label={t('sessionRail.export')}
                    >
                      <ExportIcon />
                    </button>
                    <button
                      type="button"
                      className="rail-action-btn"
                      onClick={(e) => void handleArchive(s.id, s.status, e)}
                      title={s.status === 'closed' ? t('sessionRail.unarchive') : t('sessionRail.archive')}
                      aria-label={s.status === 'closed' ? t('sessionRail.unarchive') : t('sessionRail.archive')}
                    >
                      {s.status === 'closed' ? <UnarchiveIcon /> : <ArchiveIcon />}
                    </button>
                    <button
                      type="button"
                      className={`rail-action-btn rail-action-delete ${isConfirming ? 'confirming' : ''}`}
                      onClick={(e) => void handleDelete(s.id, e)}
                      title={isConfirming ? t('sessionRail.confirmDelete') : t('sessionRail.delete')}
                      aria-label={isConfirming ? t('sessionRail.confirmDelete') : t('sessionRail.delete')}
                    >
                      {isConfirming ? <ConfirmCheckIcon /> : <TrashIcon />}
                    </button>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      </aside>
    </>
  )
}
