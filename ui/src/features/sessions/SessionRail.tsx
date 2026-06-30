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

  const load = useCallback(() => {
    fetchJSON<SessionSummary[]>('/api/v1/sessions')
      .then(rows => {
        // Active sessions only, newest first (the server already sorts by
        // updated_at desc, but guard against nulls).
        setSessions((rows ?? []).filter(s => s.status !== 'closed'))
        setError(false)
      })
      .catch(() => setError(true))
  }, [])

  // Reload whenever the rail opens or a session is created/renamed elsewhere.
  useEffect(() => {
    if (open) load()
  }, [open, refreshKey, load])

  return (
    <>
      <button
        className="rail-toggle"
        onClick={onToggle}
        title={open ? 'Sohbet listesini kapat' : 'Sohbet geçmişi'}
        aria-label="Sohbet geçmişi"
      >
        {open ? '‹' : '☰'}
      </button>

      <aside className={`session-rail ${open ? 'open' : ''}`}>
        <div className="rail-header">
          <span className="rail-title">Sohbetler</span>
        </div>
        <div className="rail-list">
          {error && <div className="rail-empty">Sohbetler yüklenemedi.</div>}
          {!error && sessions.length === 0 && (
            <div className="rail-empty">Henüz kayıtlı sohbet yok.</div>
          )}
          {sessions.map(s => (
            <button
              key={s.id}
              className={`rail-item ${s.id === activeSessionID ? 'active' : ''}`}
              onClick={() => onSelect(s.id)}
              title={s.name}
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
