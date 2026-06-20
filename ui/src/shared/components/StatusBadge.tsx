import type { ModuleStatus } from '../../entities/types'

type Props = { status: ModuleStatus['status'] }

export function StatusBadge({ status }: Props): JSX.Element {
  const cls = `status-badge badge-${status.replace(/[^a-z_]/g, '_')}`
  return (
    <span className={cls}>
      <span className={dotClass(status)} />
      {status.replace('_', ' ')}
    </span>
  )
}

function dotClass(status: string): string {
  switch (status) {
    case 'healthy':       return 'dot dot-ok'
    case 'degraded':      return 'dot dot-warn'
    case 'down':          return 'dot dot-err'
    case 'not_connected': return 'dot dot-muted'
    default:              return 'dot dot-dim'
  }
}
