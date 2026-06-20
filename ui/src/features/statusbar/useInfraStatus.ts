import { useEffect, useState } from 'react'
import { fetchJSON } from '../../shared/api/http'
import type { InfraState } from '../../entities/types'

type SandboxResponse = { status?: string }
type GatewayResponse = { status?: string; level?: number }

export function useInfraStatus(intervalMs = 5000): InfraState {
  const [state, setState] = useState<InfraState>({
    sandboxStatus: 'checking',
    gatewayStatus: 'checking',
    gatewayLevel: '-'
  })

  useEffect(() => {
    let cancelled = false

    const refresh = async () => {
      try {
        const [sandbox, gateway] = await Promise.all([
          fetchJSON<SandboxResponse>('/api/v1/sandbox/status'),
          fetchJSON<GatewayResponse>('/api/v1/gateway/status')
        ])
        if (cancelled) return
        setState({
          sandboxStatus: sandbox.status ?? 'unknown',
          gatewayStatus: gateway.status ?? 'unknown',
          gatewayLevel: String(gateway.level ?? '-')
        })
      } catch {
        if (cancelled) return
        setState((prev) => ({ ...prev, sandboxStatus: 'error', gatewayStatus: 'error' }))
      }
    }

    void refresh()
    const timer = window.setInterval(() => {
      void refresh()
    }, intervalMs)

    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [intervalMs])

  return state
}
