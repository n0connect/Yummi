import { useEffect, useState, useCallback } from 'react'
import type { GatewayRule, GatewayAuditRow, GatewayStatus, GatewayEgressMode, GatewayEgressModeResponse } from '../../entities/types'
import { apiURL } from '../../shared/api/runtime'

const POLL_MS = 5_000

export function useGatewayStatus() {
  const [gateway, setGateway] = useState<GatewayStatus | null>(null)
  const refresh = useCallback(async () => {
    try {
      const res = await fetch(apiURL('/api/v1/gateway/status'))
      if (res.ok) setGateway(await res.json() as GatewayStatus)
    } catch {
      setGateway({ status: 'disconnected', level: 0, rules_active: false, policy_kernel_consistent: false, enforcement_generation: 0 })
    }
  }, [])
  usePolling(refresh)
  return { gateway, refresh }
}

// usePolling runs refresh once, then every intervalMs, cleaning up on
// unmount — the exact "call refresh + setInterval + clearInterval" shape
// every hook below previously duplicated.
function usePolling(refresh: () => void, intervalMs = POLL_MS): void {
  useEffect(() => {
    refresh()
    const id = setInterval(refresh, intervalMs)
    return () => clearInterval(id)
  }, [refresh, intervalMs])
}

export function useGatewayRules() {
  const [rules, setRules] = useState<GatewayRule[]>([])
  const [status, setStatus] = useState<GatewayStatus['status']>('not_configured')

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(apiURL('/api/v1/gateway/rules'))
      if (res.ok) {
        const data = await res.json()
        setRules(data.rules ?? [])
        setStatus(data.status ?? 'connected')
      }
    } catch {
      setStatus('disconnected')
    }
  }, [])

  usePolling(refresh)

  return { rules, status, refresh }
}

export function useGatewayAudit(limit = 50) {
  const [rows, setRows] = useState<GatewayAuditRow[]>([])
  const [chainVerified, setChainVerified] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(apiURL(`/api/v1/gateway/audit?limit=${limit}`))
      if (res.ok) {
        const data = await res.json()
        setRows(data.rows ?? [])
        setChainVerified(Boolean(data.chain_verified))
      }
    } catch {
      // keep last known rows; status surfaced via the rules hook
    }
  }, [limit])

  usePolling(refresh)

  return { rows, chainVerified, refresh }
}

export function useEgressMode() {
  const [mode, setModeState] = useState<GatewayEgressMode>('block_all')
  const [status, setStatus] = useState<GatewayEgressModeResponse['status']>('not_configured')

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(apiURL('/api/v1/gateway/egress-mode'))
      if (res.ok) {
        const data: GatewayEgressModeResponse = await res.json()
        if (data.mode) setModeState(data.mode)
        setStatus(data.status ?? 'connected')
      }
    } catch {
      setStatus('unreachable')
    }
  }, [])

  const setMode = useCallback(async (m: GatewayEgressMode) => {
    await setEgressMode(m)
    setModeState(m) // optimistic
    await refresh()
  }, [refresh])

  usePolling(refresh)

  return { mode, status, setMode }
}

export async function setEgressMode(mode: GatewayEgressMode): Promise<void> {
  const res = await fetch(apiURL('/api/v1/gateway/egress-mode'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode })
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`failed to set egress mode: ${text}`)
  }
}

export async function addGatewayRule(network: string, ttlSecs: number, reason: string): Promise<void> {
  const res = await fetch(apiURL('/api/v1/gateway/rules'), {
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
  const res = await fetch(apiURL(`/api/v1/gateway/rules/${encodeURIComponent(id)}`), { method: 'DELETE' })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`failed to remove rule: ${text}`)
  }
}

// ─── Hostname policy ──────────────────────────────────────────────────────────

export type HostnameList = 'allow' | 'block'

export function useHostnamePolicy() {
  const [allow, setAllow] = useState<string[]>([])
  const [block, setBlock] = useState<string[]>([])
  const [status, setStatus] = useState<GatewayStatus['status']>('not_configured')

  const apply = useCallback((data: { allow?: string[]; block?: string[]; status?: string }) => {
    setAllow(data.allow ?? [])
    setBlock(data.block ?? [])
    setStatus((data.status as GatewayStatus['status']) ?? 'connected')
  }, [])

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(apiURL('/api/v1/gateway/hostname-policy'))
      if (res.ok) apply(await res.json())
    } catch {
      setStatus('disconnected')
    }
  }, [apply])

  // add/remove return the updated lists, so apply them directly (no refetch).
  const add = useCallback(async (list: HostnameList, pattern: string) => {
    apply(await editHostnameRule('POST', list, pattern))
  }, [apply])
  const remove = useCallback(async (list: HostnameList, pattern: string) => {
    apply(await editHostnameRule('DELETE', list, pattern))
  }, [apply])

  usePolling(refresh)

  return { allow, block, status, add, remove }
}

async function editHostnameRule(method: 'POST' | 'DELETE', list: HostnameList, pattern: string) {
  const res = await fetch(apiURL('/api/v1/gateway/hostname-policy'), {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ list, pattern })
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`failed to ${method === 'POST' ? 'add' : 'remove'} hostname pattern: ${text}`)
  }
  return res.json()
}
