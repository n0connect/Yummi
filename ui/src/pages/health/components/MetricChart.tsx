import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import type { MetricSample } from '../../../features/health/useHealthMetrics'

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
  return (
    <div className="metric-chart">
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
            labelFormatter={(ts: any) => fmtTime(ts)}
            formatter={(v: any) => [formatValue ? formatValue(v) : `${v}${unit}`, label]}
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
