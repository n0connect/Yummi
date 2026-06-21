import type { ModuleStatus } from '../../entities/types'
import { dotClass } from '../lib/statusDot'

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
