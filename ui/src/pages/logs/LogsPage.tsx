import { DebugLogPanel } from '../../features/debuglog/DebugLogPanel'

export function LogsPage(): JSX.Element {
  return (
    <main className="page-wrap">
      <div className="page-card">
        <DebugLogPanel />
      </div>
    </main>
  )
}
