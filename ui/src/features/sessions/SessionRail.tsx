import { useEffect, useState, useCallback, useMemo } from 'react'
import type { SessionSummary } from '../../entities/types'
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

  const load = useCallback(() => {
    const controller = new AbortController()
    setLoading(true)
    fetchJSON<SessionSummary[]>('/api/v1/sessions', { signal: controller.signal })
      .then(rows => {
        // Active sessions only, newest first (the server already sorts by
        // updated_at desc, but guard against nulls).
        setSessions((rows ?? []).filter(s => s.status !== 'closed'))
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

  const filteredSessions = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase('tr-TR')
    if (!needle) return sessions
    return sessions.filter(session => session.name.toLocaleLowerCase('tr-TR').includes(needle))
  }, [query, sessions])

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
              placeholder={t('sessionRail.searchPlaceholder')}
              aria-label={t('sessionRail.searchPlaceholder')}
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
          {!error && !loading && sessions.length > 0 && filteredSessions.length === 0 && (
            <div className="rail-empty">{t('sessionRail.noSearchResults')}</div>
          )}
          {filteredSessions.map(s => (
            <button
              key={s.id}
              className={`rail-item ${s.id === activeSessionID ? 'active' : ''}`}
              onClick={() => onSelect(s.id)}
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
              <span className="rail-item-when">{shortWhen(s.updated_at)}</span>
            </button>
          ))}
        </div>
      </aside>
    </>
  )
}
