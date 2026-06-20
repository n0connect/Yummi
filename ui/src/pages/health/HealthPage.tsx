import { useState } from 'react'
import { useHealthMetrics } from '../../features/health/useHealthMetrics'
import { MetricChart } from './components/MetricChart'
import { ModuleStatusPanel } from './components/ModuleStatusPanel'
import './HealthPage.css'

const WINDOWS = ['5m', '15m', '1h'] as const
type Window = typeof WINDOWS[number]

export function HealthPage() {
  const [window, setWindow] = useState<Window>('5m')
  const data = useHealthMetrics(window)
  const samples = data?.samples ?? []
  const latest = samples[samples.length - 1]

  return (
    <div className="health-page">
      <div className="health-header">
        <h2 className="health-title">System Health</h2>
        <div className="window-selector">
          {WINDOWS.map(w => (
            <button
              key={w}
              className={`window-btn ${window === w ? 'active' : ''}`}
              onClick={() => setWindow(w)}
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
          label="Go Heap (MB)"
          color="var(--accent)"
          unit=" MB"
          formatValue={(v) => `${v.toFixed(1)} MB`}
        />
        <MetricChart
          samples={samples}
          dataKey="ollama_lat_ms"
          label="Ollama Latency (ms)"
          color="var(--orange)"
          unit=" ms"
          formatValue={(v) => v < 0 ? 'offline' : `${v} ms`}
        />
        <MetricChart
          samples={samples}
          dataKey="goroutines"
          label="Goroutines"
          color="var(--green)"
          formatValue={(v) => String(Math.round(v))}
        />
        <ModuleStatusPanel latest={latest} />
      </div>

      {samples.length === 0 && (
        <div className="health-empty">Collecting metrics…</div>
      )}
    </div>
  )
}
