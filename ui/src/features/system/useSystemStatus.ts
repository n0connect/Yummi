import { useState, useEffect, useRef } from 'react'
import type { SystemStatusResponse } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'

const POLL_MS = 10_000

export function useSystemStatus(): SystemStatusResponse | null {
  const [data, setData] = useState<SystemStatusResponse | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    let cancelled = false

    const poll = () => {
      fetchJSON<SystemStatusResponse>('/api/v1/system/status')
        .then(d => { if (!cancelled) setData(d) })
        .catch(() => {/* keep last known value */})
        .finally(() => {
          if (!cancelled) {
            timerRef.current = setTimeout(poll, POLL_MS)
          }
        })
    }

    poll()

    return () => {
      cancelled = true
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  return data
}
