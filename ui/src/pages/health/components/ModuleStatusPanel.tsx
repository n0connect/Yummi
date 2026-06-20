import type { MetricSample } from '../../../features/health/useHealthMetrics'

type Module = { name: string; up: boolean }

function getModules(sample: MetricSample | undefined): Module[] {
  if (!sample) return [
    { name: 'sandbox', up: false },
    { name: 'gateway', up: false },
    { name: 'ollama', up: false },
  ]
  return [
    { name: 'sandbox', up: sample.sandbox_up },
    { name: 'gateway', up: sample.gateway_up },
    { name: 'ollama', up: sample.ollama_up },
  ]
}

type Props = { latest: MetricSample | undefined }

export function ModuleStatusPanel({ latest }: Props) {
  const modules = getModules(latest)
  return (
    <div className="module-status-panel">
      <div className="metric-chart-title">Module Status</div>
      <div className="module-status-grid">
        {modules.map(m => (
          <div key={m.name} className={`module-status-card ${m.up ? 'up' : 'down'}`}>
            <span className={`dot ${m.up ? 'dot-ok' : 'dot-err'}`} />
            <span className="module-status-name">{m.name}</span>
            <span className="module-status-label">{m.up ? 'healthy' : 'down'}</span>
          </div>
        ))}
        {latest && (
          <div className="module-status-card meta">
            <span className="module-status-name">goroutines</span>
            <span className="module-status-label">{latest.goroutines}</span>
          </div>
        )}
      </div>
    </div>
  )
}
