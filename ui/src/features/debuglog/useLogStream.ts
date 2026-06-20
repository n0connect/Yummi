import { useState, useEffect, useRef } from 'react'
import type { LogEntry, LogLevel } from '../../entities/types'

const MAX_ENTRIES = 500

export function useLogStream(): LogEntry[] {
  const [entries, setEntries] = useState<LogEntry[]>([])
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let cancelled = false

    const connect = () => {
      const es = new EventSource('/api/v1/system/logs')
      esRef.current = es

      es.onmessage = (ev) => {
        try {
          const entry = JSON.parse(ev.data) as LogEntry
          setEntries(prev => {
            const next = [...prev, entry]
            return next.length > MAX_ENTRIES ? next.slice(next.length - MAX_ENTRIES) : next
          })
        } catch {
          // ignore malformed
        }
      }

      es.onerror = () => {
        es.close()
        if (!cancelled) {
          retryTimer = setTimeout(connect, 3000)
        }
      }
    }

    connect()

    return () => {
      cancelled = true
      if (retryTimer) clearTimeout(retryTimer)
      esRef.current?.close()
    }
  }, [])

  return entries
}

export function filterEntries(entries: LogEntry[], level: LogLevel | 'ALL'): LogEntry[] {
  if (level === 'ALL') return entries
  const levels: Record<string, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }
  const minLevel = levels[level] ?? 0
  return entries.filter(e => (levels[e.level] ?? 0) >= minLevel)
}
