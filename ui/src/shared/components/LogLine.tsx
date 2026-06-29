import type { LogEntry } from '../../entities/types'

type Props = { entry: LogEntry }

// Allowlist of known log levels to a fixed CSS suffix — entry.level is a
// server-supplied string typed as 'DEBUG' | 'INFO' | 'WARN' | 'ERROR' | string
// (the trailing `| string` keeps unknown future levels from being a type
// error), so it must go through a closed-set lookup rather than being
// interpolated directly into className.
function levelClass(level: string): string {
  switch (level) {
    case 'DEBUG': return 'log-level log-level-DEBUG'
    case 'INFO':  return 'log-level log-level-INFO'
    case 'WARN':  return 'log-level log-level-WARN'
    case 'ERROR': return 'log-level log-level-ERROR'
    default:      return 'log-level log-level-UNKNOWN'
  }
}

export function LogLine({ entry }: Props): JSX.Element {
  const ts = formatTs(entry.t)

  let formattedMsg = entry.msg
  if (entry.msg === 'http' && entry.attrs) {
    const { method, path, status, duration_ms } = entry.attrs as {
      method?: string; path?: string; status?: number; duration_ms?: number
    }
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
      <span className={levelClass(entry.level)}>{entry.level}</span>
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
