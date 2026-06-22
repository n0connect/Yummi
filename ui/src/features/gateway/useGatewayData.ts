import { useEffect, useState, useCallback } from 'react'
import type { GatewayRule, GatewayAuditRow, GatewayStatus } from '../../entities/types'

const POLL_MS = 5_000

export function useGatewayRules() {
  const [rules, setRules] = useState<GatewayRule[]>([])
  const [status, setStatus] = useState<GatewayStatus['status']>('not_configured')

  const refresh = useCallback(async () => {
    try {
      const res = await fetch('/api/v1/gateway/rules')
      if (res.ok) {
        const data = await res.json()
        setRules(data.rules ?? [])
        setStatus(data.status ?? 'connected')
      }
    } catch {
      setStatus('disconnected')
    }
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, POLL_MS)
    return () => clearInterval(id)
  }, [refresh])

  return { rules, status, refresh }
}

export function useGatewayAudit(limit = 50) {
  const [rows, setRows] = useState<GatewayAuditRow[]>([])
  const [chainVerified, setChainVerified] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(`/api/v1/gateway/audit?limit=${limit}`)
      if (res.ok) {
        const data = await res.json()
        setRows(data.rows ?? [])
        setChainVerified(Boolean(data.chain_verified))
      }
    } catch {
      // keep last known rows; status surfaced via the rules hook
    }
  }, [limit])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, POLL_MS)
    return () => clearInterval(id)
  }, [refresh])

  return { rows, chainVerified, refresh }
}

export async function addGatewayRule(network: string, ttlSecs: number, reason: string): Promise<void> {
  const res = await fetch('/api/v1/gateway/rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ network, ttl_secs: ttlSecs, reason })
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`failed to add rule: ${text}`)
  }
}

export async function removeGatewayRule(id: string): Promise<void> {
  const res = await fetch(`/api/v1/gateway/rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`failed to remove rule: ${text}`)
  }
}
