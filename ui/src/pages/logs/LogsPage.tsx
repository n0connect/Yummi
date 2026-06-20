import { DebugLogPanel } from '../../features/debuglog/DebugLogPanel'

export function LogsPage(): JSX.Element {
  return (
    <div className="page-wrap">
      <div className="page-card">
        <DebugLogPanel />
      </div>
    </div>
  )
}
