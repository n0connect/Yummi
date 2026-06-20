import { useEffect, useRef, useState } from 'react'

export type MetricSample = {
  ts: number
  heap_mb: number
  goroutines: number
  ollama_lat_ms: number
  sandbox_up: boolean
  gateway_up: boolean
  ollama_up: boolean
}

export type HealthMetricsResponse = {
  samples: MetricSample[]
  window: string
  interval: number
}

const POLL_MS = 5_000

export function useHealthMetrics(window: string = '5m'): HealthMetricsResponse | null {
  const [data, setData] = useState<HealthMetricsResponse | null>(null)
  const lastRef = useRef<HealthMetricsResponse | null>(null)

  useEffect(() => {
    let cancelled = false

    async function poll() {
      try {
        const res = await fetch(`/api/v1/system/health-metrics?window=${window}`)
        if (res.ok && !cancelled) {
          const json = await res.json()
          lastRef.current = json
          setData(json)
        }
      } catch {
        // retain last known value on network error
        if (!cancelled && lastRef.current) setData(lastRef.current)
      }
    }

    poll()
    const id = setInterval(poll, POLL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [window])

  return data
}
