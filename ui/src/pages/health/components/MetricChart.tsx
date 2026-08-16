import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import type { MetricSample } from '../../../features/health/useHealthMetrics'
import { t } from '../../../shared/i18n/strings'

type Props = {
  samples: MetricSample[]
  dataKey: keyof MetricSample
  label: string
  color: string
  unit?: string
  formatValue?: (v: number) => string
}

function fmtTime(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function MetricChart({ samples, dataKey, label, color, unit = '', formatValue }: Props) {
  // recharts renders an SVG tree with no inherent screen-reader-friendly text
  // alternative, and re-drawing it on every sample would be a very noisy live
  // region. Instead: hide the chart visuals from assistive tech and expose a
  // single accessible summary (the metric name + its latest value) on the
  // wrapping element, refreshed on each render like the visual chart is.
  const latest = samples[samples.length - 1]
  const latestValue = latest ? (latest[dataKey] as number) : undefined
  const latestDisplay =
    latestValue === undefined
      ? undefined
      : formatValue
        ? formatValue(latestValue)
        : `${latestValue}${unit}`
  const chartAriaLabel = latestDisplay
    ? `${label}${t('health.chartAriaLabelSeparator')}${latestDisplay}`
    : label

  return (
    // role="img" flattens this subtree's accessible-tree presentation (like
    // native <img> alt text), so recharts' generated SVG internals are never
    // exposed to assistive tech individually — only the aria-label below is.
    <div className="metric-chart" role="img" aria-label={chartAriaLabel}>
      <div className="metric-chart-title">{label}</div>
      <ResponsiveContainer width="100%" height={140}>
        <AreaChart data={samples} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={`grad-${dataKey as string}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={color} stopOpacity={0.3} />
              <stop offset="95%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <XAxis
            dataKey="ts"
            tickFormatter={fmtTime}
            tick={{ fontSize: 10, fill: 'var(--text-muted)' }}
            tickLine={false}
            interval="preserveStartEnd"
          />
          <YAxis
            tick={{ fontSize: 10, fill: 'var(--text-muted)' }}
            tickLine={false}
            axisLine={false}
            width={40}
            tickFormatter={formatValue ?? ((v) => `${v}${unit}`)}
          />
          <Tooltip
            contentStyle={{ background: 'var(--bg-panel)', border: '1px solid var(--border)', fontSize: 11 }}
            labelFormatter={(ts) => fmtTime(Number(ts))}
            formatter={(v) => [formatValue ? formatValue(Number(v)) : `${v}${unit}`, label]}
          />
          <Area
            type="monotone"
            dataKey={dataKey as string}
            stroke={color}
            strokeWidth={1.5}
            fill={`url(#grad-${dataKey as string})`}
            dot={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
