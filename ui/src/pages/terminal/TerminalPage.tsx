import { TerminalPanel } from '../../features/terminal/TerminalPanel'

export function TerminalPage(): JSX.Element {
  return (
    <div className="page-wrap">
      <div className="page-card">
        <TerminalPanel />
      </div>
    </div>
  )
}
