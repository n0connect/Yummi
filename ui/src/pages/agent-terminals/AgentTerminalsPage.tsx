import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'
import './AgentTerminalsPage.css'

// Watch-only view of the terminals the AGENT opened in the sandbox. The
// operator cannot type into these — the page has no input element, and the
// backend only exposes the read verbs for everything else. Control over what
// RUNS in a session stays with the agent. The one exception is ending a
// session outright (the × below, gated behind a two-click confirm mirroring
// SessionRail's delete pattern) — cleanup, not steering.

type AgentTerminal = {
  id: string
  label?: string
  windows: string
  size: string
  created: string
}

type TerminalOutput = { id: string; output: string; flagged: boolean; flag_reason?: string }

const LIST_MS = 3000
const READ_MS = 1500

export function AgentTerminalsPage() {
  const [terminals, setTerminals] = useState<AgentTerminal[]>([])
  const [selected, setSelected] = useState<string>('')
  const [output, setOutput] = useState<TerminalOutput | null>(null)
  const [error, setError] = useState<string>('')
  const [follow, setFollow] = useState(true)
  const [confirmCloseId, setConfirmCloseId] = useState<string | null>(null)
  const paneRef = useRef<HTMLPreElement | null>(null)
  const confirmTimerRef = useRef<number | undefined>(undefined)

  useEffect(() => {
    let alive = true
    const load = async () => {
      try {
        const res = await fetchJSON<{ terminals: AgentTerminal[] }>('/api/v1/agent-terminals')
        if (!alive) return
        setTerminals(res.terminals)
        setError('')
        setSelected(cur => (cur && res.terminals.some(x => x.id === cur) ? cur : res.terminals[0]?.id ?? ''))
      } catch {
        if (alive) setError(t('agentTerm.loadError'))
      }
    }
    void load()
    const timer = window.setInterval(load, LIST_MS)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!selected) {
      setOutput(null)
      return
    }
    let alive = true
    const read = async () => {
      try {
        const res = await fetchJSON<TerminalOutput>(`/api/v1/agent-terminals/${encodeURIComponent(selected)}?lines=400`)
        if (alive) setOutput(res)
      } catch {
        if (alive) setError(t('agentTerm.loadError'))
      }
    }
    void read()
    const timer = window.setInterval(read, READ_MS)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [selected])

  useEffect(() => {
    if (follow && paneRef.current) paneRef.current.scrollTop = paneRef.current.scrollHeight
  }, [output, follow])

  useEffect(() => () => {
    if (confirmTimerRef.current) window.clearTimeout(confirmTimerRef.current)
  }, [])

  const handleClose = useCallback(async (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    if (confirmCloseId !== id) {
      setConfirmCloseId(id)
      if (confirmTimerRef.current) window.clearTimeout(confirmTimerRef.current)
      confirmTimerRef.current = window.setTimeout(() => setConfirmCloseId(null), 3500)
      return
    }
    if (confirmTimerRef.current) window.clearTimeout(confirmTimerRef.current)
    setConfirmCloseId(null)
    try {
      await fetchJSON(`/api/v1/agent-terminals/${encodeURIComponent(id)}`, { method: 'DELETE' })
      setTerminals(prev => prev.filter(x => x.id !== id))
      setSelected(cur => (cur === id ? '' : cur))
    } catch {
      setError(t('agentTerm.closeError'))
    }
  }, [confirmCloseId])

  return (
    <section className="agent-terminals">
      <header className="agent-terminals-header">
        <div>
          <span className="agent-terminals-eyebrow">{t('agentTerm.eyebrow')}</span>
          <h1 className="agent-terminals-title">{t('agentTerm.title')}</h1>
        </div>
        <span className="agent-terminals-readonly">{t('agentTerm.readOnlyNote')}</span>
      </header>

      {error && <p className="agent-terminals-error" role="alert">{error}</p>}

      <div className="agent-terminals-body">
        <ul className="agent-terminals-list" aria-label={t('agentTerm.listAriaLabel')}>
          {terminals.length === 0 && <li className="agent-terminals-empty">{t('agentTerm.empty')}</li>}
          {terminals.map(term => (
            <li key={term.id} className="agent-terminals-row">
              <button
                type="button"
                className={`agent-terminals-item${term.id === selected ? ' active' : ''}`}
                onClick={() => setSelected(term.id)}
                aria-pressed={term.id === selected}
              >
                <span className="agent-terminals-item-id">{term.label || term.id}</span>
                <span className="agent-terminals-item-meta">{term.size} · {term.created}</span>
              </button>
              <button
                type="button"
                className={`agent-terminals-close${confirmCloseId === term.id ? ' confirming' : ''}`}
                onClick={e => void handleClose(term.id, e)}
                title={confirmCloseId === term.id ? t('agentTerm.confirmClose') : t('agentTerm.close')}
                aria-label={confirmCloseId === term.id ? t('agentTerm.confirmClose') : t('agentTerm.close')}
              >
                <svg viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
                  <path d="M3 3 L13 13 M13 3 L3 13" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                </svg>
              </button>
            </li>
          ))}
        </ul>

        <div className="agent-terminals-pane-wrap">
          <div className="agent-terminals-pane-bar">
            <span>{selected || t('agentTerm.selectHint')}</span>
            <label className="agent-terminals-follow">
              <input type="checkbox" checked={follow} onChange={e => setFollow(e.target.checked)} />
              {t('agentTerm.follow')}
            </label>
          </div>
          {output?.flagged && <p className="agent-terminals-flag" role="status">{t('agentTerm.flagged')}</p>}
          <pre ref={paneRef} className="agent-terminals-pane" tabIndex={0}>
            {output ? output.output : ''}
          </pre>
        </div>
      </div>
    </section>
  )
}
