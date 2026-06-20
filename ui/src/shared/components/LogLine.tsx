import type { LogEntry } from '../../entities/types'

type Props = { entry: LogEntry }

export function LogLine({ entry }: Props): JSX.Element {
  const ts = formatTs(entry.t)

  let formattedMsg = entry.msg
  if (entry.msg === 'http' && entry.attrs) {
    const { method, path, status, duration_ms } = entry.attrs as any
    formattedMsg = `${method} ${path} [${status}] (${duration_ms}ms)`
  } else if (entry.attrs && Object.keys(entry.attrs).length > 0) {
    const attrStr = Object.entries(entry.attrs)
      .map(([k, v]) => `${k}=${v}`)
      .join(' ')
    formattedMsg = `${entry.msg} | ${attrStr}`
  }

  return (
    <div className="log-line">
      <span className="log-ts">{ts}</span>
      <span className={`log-level log-level-${entry.level}`}>{entry.level}</span>
      <span className="log-msg">{formattedMsg}</span>
    </div>
  )
}

function formatTs(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return iso.slice(11, 19) || '--:--:--'
  }
}
