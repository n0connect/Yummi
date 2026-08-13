import type { MetricSample } from '../../../features/health/useHealthMetrics'
import type { ModuleStatus, SystemStatusResponse } from '../../../entities/types'
import { dotClass } from '../../../shared/lib/statusDot'
import { t } from '../../../shared/i18n/strings'

const MODULE_LABEL: Record<string, string> = {
  sandbox: t('moduleStatus.label.sandbox'),
  gateway: t('moduleStatus.label.gateway'),
  orchestrator: t('moduleStatus.label.orchestrator'),
  conductor: t('moduleStatus.label.conductor'),
  docker: t('moduleStatus.label.docker'),
  ollama: t('moduleStatus.label.ollama'),
}

const STATUS_LABEL: Record<string, string> = {
  healthy: t('moduleStatus.status.healthy'),
  degraded: t('moduleStatus.status.degraded'),
  down: t('moduleStatus.status.down'),
  unknown: t('moduleStatus.status.unknown'),
  not_connected: t('moduleStatus.status.not_connected'),
  not_configured: t('moduleStatus.status.not_configured'),
}

function fallbackModules(sample: MetricSample | undefined): ModuleStatus[] {
  if (!sample) {
    return ['sandbox', 'gateway', 'ollama'].map(name => ({ name, status: 'unknown' }))
  }
  return [
    { name: 'sandbox', status: sample.sandbox_up ? 'healthy' : 'down' },
    { name: 'gateway', status: sample.gateway_up ? 'healthy' : 'down' },
    { name: 'ollama', status: sample.ollama_up ? 'healthy' : 'down' },
  ]
}

function moduleMeta(module: ModuleStatus): string | null {
  if (module.name !== 'conductor') return null
  const backend = typeof module.meta?.backend === 'string' ? module.meta.backend : null
  const model = typeof module.meta?.model === 'string' ? module.meta.model : null
  return [backend, model].filter(Boolean).join(' · ') || null
}

function overallClass(status: string): string {
  switch (status) {
    case 'healthy': return 'health-overall health-overall-healthy'
    case 'degraded': return 'health-overall health-overall-degraded'
    case 'down': return 'health-overall health-overall-down'
    default: return 'health-overall'
  }
}

type Props = {
  latest: MetricSample | undefined
  systemStatus: SystemStatusResponse | null
}

export function ModuleStatusPanel({ latest, systemStatus }: Props) {
  const modules = systemStatus?.modules?.length
    ? systemStatus.modules
    : fallbackModules(latest)

  return (
    <div className="module-status-panel">
      <div className="module-status-heading">
        <div>
          <div className="metric-chart-title">{t('moduleStatus.title')}</div>
          <p>{t('moduleStatus.subtitle')}</p>
        </div>
        {systemStatus && (
          <span className={overallClass(systemStatus.overall)}>
            {STATUS_LABEL[systemStatus.overall] ?? systemStatus.overall}
          </span>
        )}
      </div>
      <div className="module-status-grid">
        {modules.map(m => (
          <div key={m.name} className="module-status-card">
            <span className={dotClass(m.status)} />
            <span className="module-status-main">
              <span className="module-status-name">{MODULE_LABEL[m.name] ?? m.name}</span>
              {moduleMeta(m) && <span className="module-status-meta">{moduleMeta(m)}</span>}
            </span>
            <span className="module-status-label">{STATUS_LABEL[m.status] ?? t('moduleStatus.status.unknown')}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
