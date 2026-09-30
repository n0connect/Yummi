import { TerminalPanel } from '../../features/terminal/TerminalPanel'
import { BriefingPanel } from '../../features/operator/BriefingPanel'
import { SessionContextBadge } from '../../shared/lib/SessionContextBadge'
import './TerminalPage.css'

// The operator's manual workbench: they drive on the left (a real shell into
// the sandbox), and Yummi's accumulated knowledge serves them on the right
// (the operator briefing). This is the surface where "operator drives, AI
// assists" is literal — the human runs commands by hand and pulls what Yummi
// already knows on demand, rather than asking an agent to act for them.
export function TerminalPage(): JSX.Element {
  return (
    <main className="page-wrap workbench">
      <div className="page-card workbench-terminal">
        <div className="workbench-session-strip">
          <SessionContextBadge />
        </div>
        <TerminalPanel />
      </div>
      <div className="workbench-briefing">
        <BriefingPanel />
      </div>
    </main>
  )
}
