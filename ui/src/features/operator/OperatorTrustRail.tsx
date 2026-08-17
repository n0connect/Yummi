import { useCallback, useEffect, useMemo, useState } from 'react'
import { NavLink } from 'react-router-dom'
import type { AgentCapabilities, AgentDelegationRun, GatewayStatus } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'
import { useChatStream, type EventConnectionState } from '../chat/useChatStream'
import { deriveTrustState } from './operatorTrust'

const readableCapability = (value: string) => value.replace(/_/g, ' ')

export function OperatorTrustRail(): JSX.Element {
  const [events, setEvents] = useState<EventConnectionState>('connecting')
  const [gateway, setGateway] = useState<GatewayStatus | null>(null)
  const [caps, setCaps] = useState<AgentCapabilities | null>(null)
  const [delegations, setDelegations] = useState<AgentDelegationRun[]>([])

  const refresh = useCallback(() => {
    fetchJSON<GatewayStatus>('/api/v1/gateway/status').then(setGateway).catch(() => {
      setGateway({ status: 'disconnected', level: 0, rules_active: false, policy_kernel_consistent: false, enforcement_generation: 0 })
    })
    fetchJSON<AgentCapabilities>('/api/v1/agent/capabilities').then(setCaps).catch(() => setCaps(null))
    fetchJSON<AgentDelegationRun[]>('/api/v1/agent/delegations').then(setDelegations).catch(() => setDelegations([]))
  }, [])

  useEffect(() => {
    refresh()
    const timer = window.setInterval(refresh, 5_000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useChatStream({ onConnectionState: setEvents, onReconnect: refresh })
  const trust = deriveTrustState(events, gateway)
  const activeDelegations = useMemo(() => delegations.filter(run => run.status === 'running').length, [delegations])

  return (
    <aside className={`trust-rail trust-${trust}`} aria-label={t('trustRail.ariaLabel')}>
      <span className="trust-rail-state" role="status" aria-live="polite">
        <span className="trust-rail-beacon" aria-hidden="true" />
        {t(`trustRail.state.${trust}`)}
      </span>
      <span className="trust-rail-divider" aria-hidden="true" />
      <span className="trust-rail-item">
        <span className="trust-rail-key">{t('trustRail.events')}</span>
        <span>{t(`trustRail.events.${events}`)}</span>
      </span>
      <NavLink className="trust-rail-item trust-rail-link" to="/trace">
        <span className="trust-rail-key">{t('trustRail.agent')}</span>
        <span>{caps ? `${caps.backend} · ${readableCapability(caps.recovery_model)}` : t('trustRail.unavailable')}</span>
      </NavLink>
      <NavLink className="trust-rail-item trust-rail-link" to="/gateway">
        <span className="trust-rail-key">{t('trustRail.gateway')}</span>
        <span>{gateway?.policy_kernel_consistent ? `${t('trustRail.synced')} · g${gateway.enforcement_generation}` : t('trustRail.notSynced')}</span>
      </NavLink>
      <span className="trust-rail-item trust-rail-grow">
        <span className="trust-rail-key">{t('trustRail.knowledge')}</span>
        <span>{caps ? `${caps.memory_enabled ? t('trustRail.memoryOn') : t('trustRail.memoryOff')} · ${caps.skill_count} ${t('trustRail.skills')}` : t('trustRail.unavailable')}</span>
      </span>
      <span className="trust-rail-item">
        <span className="trust-rail-key">{t('trustRail.subAgents')}</span>
        <span>{activeDelegations}/{caps?.max_concurrent_sub_agents ?? 0}</span>
      </span>
    </aside>
  )
}
