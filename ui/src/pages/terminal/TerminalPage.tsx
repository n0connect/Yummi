import { useState } from 'react'
import { TerminalPanel } from '../../features/terminal/TerminalPanel'
import { BriefingPanel } from '../../features/operator/BriefingPanel'
import { SessionContextBadge } from '../../shared/lib/SessionContextBadge'
import './TerminalPage.css'
import { t } from '../../shared/i18n/strings'

// The operator's manual workbench: they drive on the left (a real shell into
// the sandbox), and Yummi's accumulated knowledge serves them on the right
// (the operator briefing). This is the surface where "operator drives, AI
// assists" is literal — the human runs commands by hand and pulls what Yummi
// already knows on demand, rather than asking an agent to act for them.
export function TerminalPage(): JSX.Element {
  const [briefingOpen, setBriefingOpen] = useState(() => {
    try { return localStorage.getItem('yummi:terminal:briefing') !== 'hidden' } catch { return true }
  })
  const toggleBriefing = () => {
    const next = !briefingOpen
    setBriefingOpen(next)
    try { localStorage.setItem('yummi:terminal:briefing', next ? 'shown' : 'hidden') } catch { /* Optional layout preference. */ }
  }
  return (
    <main className={`page-wrap workbench ${briefingOpen ? '' : 'workbench-wide'}`}>
      <div className="page-card workbench-terminal">
        <div className="workbench-session-strip">
          <span className="workbench-session-context"><SessionContextBadge /><span>{t('terminal.sharedSession')}</span></span>
          <button type="button" className="small-button" aria-expanded={briefingOpen} aria-controls="operator-briefing" onClick={toggleBriefing}>{briefingOpen ? t('terminal.hideBriefing') : t('terminal.showBriefing')}</button>
        </div>
        <TerminalPanel />
      </div>
      <div className="workbench-briefing" id="operator-briefing" hidden={!briefingOpen}>
        <BriefingPanel />
      </div>
    </main>
  )
}
