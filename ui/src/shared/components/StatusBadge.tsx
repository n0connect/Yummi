import type { ModuleStatus } from '../../entities/types'
import { badgeClass, dotClass } from '../lib/statusDot'

type Props = { status: ModuleStatus['status'] }

export function StatusBadge({ status }: Props): JSX.Element {
  return (
    <span className={badgeClass(status)}>
      <span className={dotClass(status)} />
      {status.replace('_', ' ')}
    </span>
  )
}
