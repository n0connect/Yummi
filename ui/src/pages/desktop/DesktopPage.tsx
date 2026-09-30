import { DesktopPanel } from '../../features/desktop/DesktopPanel'

export function DesktopPage(): JSX.Element {
  return (
    <main className="page-wrap">
      <div className="page-card">
        <DesktopPanel />
      </div>
    </main>
  )
}
