import type { ModuleStatus } from '../../entities/types'
import { StatusBadge } from './StatusBadge'
import { MetricRow } from './MetricRow'

type Props = { module: ModuleStatus }

export function ModuleCard({ module }: Props): JSX.Element {
  return (
    <div className="module-card">
      <div className="module-card-header">
        <span className="module-card-name">{module.name}</span>
        <StatusBadge status={module.status} />
      </div>
      <div className="module-card-details">{module.details ?? ''}</div>
      {module.uptime_secs !== undefined && (
        <MetricRow label="uptime" value={formatUptime(module.uptime_secs)} />
      )}
      {module.meta && Object.entries(module.meta).map(([k, v]) => (
        <MetricRow key={k} label={k} value={String(v)} />
      ))}
    </div>
  )
}

function formatUptime(secs: number): string {
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  return `${h}h ${m}m`
}
