import { useCallback, useState } from 'react'
import { TerminalPanel } from '../terminal/TerminalPanel'
import { t } from '../../shared/i18n/strings'
import './MiniSandboxPreview.css'

// MiniSandboxPreview is purely a "feeling" — not a way to watch the agent
// work (the agent never types into a visible terminal; its own commands run
// as one-shot execs). What it DOES honestly show: the SAME persistent
// tmux session the Bash tab owns, in the SAME container the agent's own
// commands run in (both target YUMMI_SANDBOX_CONTAINER) — so a file the
// agent just created really is `ls`-able from here. A small, collapsed-by-
// default ambient presence the operator can peek into, not a mandatory
// live feed.
export function MiniSandboxPreview(): JSX.Element {
  const [expanded, setExpanded] = useState(false)
  const [connected, setConnected] = useState(false)
  // TerminalPanel only mounts (and opens its WS) once expanded — no reason
  // to hold a docker exec / tmux attach open for a widget nobody opened.
  const [everExpanded, setEverExpanded] = useState(false)

  const onConnectedChange = useCallback((c: boolean) => setConnected(c), [])

  const toggle = () => {
    setExpanded((e) => !e)
    setEverExpanded(true)
  }

  return (
    <div className={`mini-sandbox ${expanded ? 'mini-sandbox-expanded' : ''}`}>
      <button
        type="button"
        className="mini-sandbox-pill"
        onClick={toggle}
        title={t('assistant.miniSandboxToggle')}
        aria-expanded={expanded}
      >
        <span className={`dot ${expanded && connected ? 'dot-ok' : 'dot-err'}`} aria-hidden="true" />
        {t('assistant.miniSandboxLabel')}
        <span className="mini-sandbox-caret" aria-hidden="true">{expanded ? '▾' : '▸'}</span>
      </button>
      {expanded && (
        <div className="mini-sandbox-body">
          {everExpanded && <TerminalPanel compact onConnectedChange={onConnectedChange} />}
        </div>
      )}
    </div>
  )
}
