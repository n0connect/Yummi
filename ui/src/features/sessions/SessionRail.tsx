import { useEffect, useState, useCallback } from 'react'
import type { SessionSummary } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import './SessionRail.css'

type Props = {
  open: boolean
  onToggle: () => void
  activeSessionID: string
  // refreshKey bumps when AssistantPanel creates/renames a session, so the
  // list reflects a brand-new conversation and its auto-title without polling.
  refreshKey: number
  onSelect: (sessionID: string) => void
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
export function SessionRail({ open, onToggle, activeSessionID, refreshKey, onSelect }: Props): JSX.Element {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(false)

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

  return (
    <>
      <button
        className={`rail-toggle ${open ? 'open' : ''}`}
        onClick={onToggle}
        title={open ? 'Sohbet listesini kapat' : 'Sohbet geçmişi'}
        aria-label="Sohbet geçmişi"
        aria-expanded={open}
      >
        <span className="rail-toggle-icon" aria-hidden="true">{open ? '‹' : '🕘'}</span>
        Geçmiş
      </button>

      <aside className={`session-rail ${open ? 'open' : ''}`} role="dialog" aria-label="Sohbet geçmişi" aria-modal="false">
        <div className="rail-header">
          <span className="rail-title">Sohbetler</span>
        </div>
        <div className="rail-list">
          {loading && sessions.length === 0 && !error && (
            <div className="rail-empty">Yükleniyor…</div>
          )}
          {error && (
            <div className="rail-empty rail-error">
              <span>Sohbetler yüklenemedi.</span>
              <button className="rail-retry" onClick={() => load()}>Tekrar dene</button>
            </div>
          )}
          {!error && !loading && sessions.length === 0 && (
            <div className="rail-empty">Henüz kayıtlı sohbet yok.</div>
          )}
          {sessions.map(s => (
            <button
              key={s.id}
              className={`rail-item ${s.id === activeSessionID ? 'active' : ''}`}
              onClick={() => onSelect(s.id)}
              title={s.name}
              aria-current={s.id === activeSessionID ? 'true' : undefined}
            >
              <span className="rail-item-name">{s.name}</span>
              <span className="rail-item-when">{shortWhen(s.updated_at)}</span>
            </button>
          ))}
        </div>
      </aside>
    </>
  )
}
