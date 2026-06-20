type Props = {
  label: string
  value: string | number | undefined
}

export function MetricRow({ label, value }: Props): JSX.Element | null {
  if (value === undefined || value === null) return null
  return (
    <div className="metric-row">
      <span className="metric-key">{label}</span>
      <span className="metric-value">{String(value)}</span>
    </div>
  )
}
