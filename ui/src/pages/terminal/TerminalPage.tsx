import { TerminalPanel } from '../../features/terminal/TerminalPanel'

export function TerminalPage(): JSX.Element {
  return (
    <main className="page-wrap">
      <div className="page-card">
        <TerminalPanel />
      </div>
    </main>
  )
}
