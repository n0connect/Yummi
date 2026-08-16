import { useState } from 'react'
import { useHealthMetrics } from '../../features/health/useHealthMetrics'
import { useSystemStatus } from '../../features/system/useSystemStatus'
import { MetricChart } from './components/MetricChart'
import { ModuleStatusPanel } from './components/ModuleStatusPanel'
import { t } from '../../shared/i18n/strings'
import './HealthPage.css'

const WINDOWS = ['5m', '15m', '1h'] as const
type MetricWindow = typeof WINDOWS[number]

export function HealthPage() {
  const [timeWindow, setTimeWindow] = useState<MetricWindow>('5m')
  const data = useHealthMetrics(timeWindow)
  const systemStatus = useSystemStatus()
  const samples = data?.samples ?? []
  const latest = samples[samples.length - 1]

  return (
    <main className="health-page">
      <div className="health-header">
        <div>
          <span className="health-eyebrow">{t('health.eyebrow')}</span>
          <h2 className="health-title">{t('health.title')}</h2>
        </div>
        <div className="window-selector" role="group" aria-label={t('health.windowSelectorAriaLabel')}>
          {WINDOWS.map(w => (
            <button
              key={w}
              className={`window-btn ${timeWindow === w ? 'active' : ''}`}
              aria-pressed={timeWindow === w}
              onClick={() => setTimeWindow(w)}
            >
              {w}
            </button>
          ))}
        </div>
      </div>

      <div className="health-grid">
        <MetricChart
          samples={samples}
          dataKey="heap_mb"
          label={t('health.heapLabel')}
          color="var(--accent)"
          unit=" MB"
          formatValue={(v) => `${v.toFixed(1)} MB`}
        />
        <MetricChart
          samples={samples}
          dataKey="ollama_lat_ms"
          label={t('health.ollamaLatencyLabel')}
          color="var(--orange)"
          unit=" ms"
          formatValue={(v) => v < 0 ? t('health.offline') : `${v} ms`}
        />
        <MetricChart
          samples={samples}
          dataKey="goroutines"
          label={t('health.goroutineLabel')}
          color="var(--green)"
          formatValue={(v) => String(Math.round(v))}
        />
        <ModuleStatusPanel latest={latest} systemStatus={systemStatus} />
      </div>

      {samples.length === 0 && (
        <div className="health-empty" role="status" aria-live="polite">{t('health.metricsCollecting')}</div>
      )}
    </main>
  )
}
