import { useState, useEffect } from 'react'
import { useGatewayRules, useGatewayAudit, addGatewayRule, removeGatewayRule, useEgressMode, useHostnamePolicy, type HostnameList } from '../../features/gateway/useGatewayData'
import type { GatewayEgressMode } from '../../entities/types'
import { apiURL } from '../../shared/api/runtime'
import { t } from '../../shared/i18n/strings'
import './GatewayPage.css'

const EGRESS_MODES: { mode: GatewayEgressMode; label: string; hint: string }[] = [
  { mode: 'block', label: t('gateway.egress.block.label'), hint: t('gateway.egress.block.hint') },
  { mode: 'allow', label: t('gateway.egress.allow.label'), hint: t('gateway.egress.allow.hint') },
  { mode: 'ask', label: t('gateway.egress.ask.label'), hint: t('gateway.egress.ask.hint') }
]

// HostnameColumn renders one allow/block pattern list with per-item remove
// buttons — GatewayPage previously duplicated this JSX once per list with
// only the label/CSS-modifier/data differing.
function HostnameColumn({
  list,
  labelKey,
  patterns,
  onRemove
}: {
  list: HostnameList
  labelKey: 'gateway.allowListLabel' | 'gateway.blockListLabel'
  patterns: string[]
  onRemove: (list: HostnameList, pattern: string) => void
}): JSX.Element {
  return (
    <div className="gateway-hostname-col">
      <span className={`gateway-hostname-label gateway-hostname-${list}`}>{t(labelKey)} ({list})</span>
      {patterns.length === 0 && <span className="gateway-empty">{t('gateway.emptyDash')}</span>}
      {patterns.map(p => (
        <div key={`${list}-${p}`} className="gateway-hostname-item">
          <code>{p}</code>
          <button onClick={() => onRemove(list, p)} title={t('common.remove')} aria-label={`${t('common.remove')} ${p}`}>✕</button>
        </div>
      ))}
    </div>
  )
}

export function GatewayPage(): JSX.Element {
  const { rules, status, refresh: refreshRules } = useGatewayRules()
  const { rows, chainVerified } = useGatewayAudit(50)
  const { mode: egressMode, status: egressStatus, setMode: setEgress } = useEgressMode()
  const hostnamePolicy = useHostnamePolicy()
  const [hnPattern, setHnPattern] = useState('')
  const [hnList, setHnList] = useState<HostnameList>('block')
  const [hnError, setHnError] = useState<string | null>(null)

  async function handleAddHostname() {
    const p = hnPattern.trim()
    if (!p) return
    setHnError(null)
    try {
      await hostnamePolicy.add(hnList, p)
      setHnPattern('')
    } catch (err) {
      setHnError(err instanceof Error ? err.message : 'failed to add pattern')
    }
  }
  async function handleRemoveHostname(list: HostnameList, pattern: string) {
    setHnError(null)
    try {
      await hostnamePolicy.remove(list, pattern)
    } catch (err) {
      setHnError(err instanceof Error ? err.message : 'failed to remove pattern')
    }
  }
  const [egressBusy, setEgressBusy] = useState(false)
  const [egressError, setEgressError] = useState<string | null>(null)
  const egressReachable = egressStatus === 'connected'

  async function handleEgressChange(m: GatewayEgressMode) {
    if (m === egressMode || egressBusy) return
    setEgressBusy(true)
    setEgressError(null)
    try {
      await setEgress(m)
    } catch (err) {
      setEgressError(err instanceof Error ? err.message : 'failed to set egress mode')
    } finally {
      setEgressBusy(false)
    }
  }
  const [network, setNetwork] = useState('')
  const [ttl, setTtl] = useState('600')
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [fingerprint, setFingerprint] = useState<{ ttl_normalize: number | null; mac_randomization: boolean } | null>(null)

  useEffect(() => {
    fetch(apiURL('/api/v1/gateway/fingerprint'))
      .then(res => res.ok ? res.json() : null)
      .then(data => { if (data) setFingerprint(data) })
      .catch(() => {})
  }, [])

  const canSubmit = network.trim() !== '' && reason.trim() !== '' && !submitting

  async function handleAddRule() {
    setSubmitting(true)
    setFormError(null)
    try {
      await addGatewayRule(network.trim(), Number(ttl), reason.trim())
      setNetwork('')
      setReason('')
      await refreshRules()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'failed to add rule')
    } finally {
      setSubmitting(false)
    }
  }

  async function handleRemove(id: string) {
    await removeGatewayRule(id)
    await refreshRules()
  }

  if (status === 'disconnected') {
    return (
      <main className="gateway-page">
        <div className="gateway-card gateway-unreachable">
          <span className="gateway-unreachable-mark" aria-hidden="true">⌁</span>
          <div>
            <h2>{t('gateway.unreachableTitle')}</h2>
            <p>{t('gateway.unreachableBody')}</p>
          </div>
          <button className="small-button" onClick={() => void refreshRules()}>{t('common.retry')}</button>
        </div>
      </main>
    )
  }

  return (
    <main className="gateway-page">
      <div className="gateway-header">
        <h2 className="gateway-title">{t('gateway.title')}</h2>
        <span className={`gateway-status-badge ${status}`} role="status" aria-live="polite">{status}</span>
      </div>

      <div className="gateway-card gateway-egress-card">
        <h3>{t('gateway.egressModeTitle')}</h3>
        <div className="gateway-egress-selector" role="group" aria-label={t('gateway.egressModeGroupAriaLabel')}>
          {EGRESS_MODES.map(({ mode, label }) => (
            <button
              key={mode}
              type="button"
              className={`gateway-egress-btn ${egressMode === mode ? 'active' : ''}`}
              disabled={!egressReachable || egressBusy}
              aria-pressed={egressMode === mode}
              onClick={() => handleEgressChange(mode)}
            >
              {label}
            </button>
          ))}
        </div>
        {egressReachable ? (
          <p className="gateway-egress-hint">
            {EGRESS_MODES.find(m => m.mode === egressMode)?.hint}
          </p>
        ) : (
          <p className="gateway-egress-status">
            {egressStatus === 'not_configured' ? t('gateway.notConfigured') : t('gateway.unreachableShort')}
          </p>
        )}
        {egressError && <div className="gateway-form-error" role="alert">{egressError}</div>}
      </div>

      <div className="gateway-card">
        <h3>{t('gateway.hostnamePolicyTitle')}</h3>
        <p className="gateway-egress-hint">
          {t('gateway.hostnamePolicyDesc')}
        </p>
        <div className="gateway-hostname-lists">
          <HostnameColumn list="allow" labelKey="gateway.allowListLabel" patterns={hostnamePolicy.allow} onRemove={handleRemoveHostname} />
          <HostnameColumn list="block" labelKey="gateway.blockListLabel" patterns={hostnamePolicy.block} onRemove={handleRemoveHostname} />
        </div>
        <div className="gateway-add-form">
          <select value={hnList} onChange={e => setHnList(e.target.value as HostnameList)} aria-label={t('gateway.hostnameListSelectAriaLabel')}>
            <option value="block">{t('gateway.selectBlock')}</option>
            <option value="allow">{t('gateway.selectAllow')}</option>
          </select>
          <input
            placeholder={t('gateway.hostnamePlaceholder')}
            aria-label={t('gateway.hostnamePlaceholder')}
            value={hnPattern}
            onChange={e => setHnPattern(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') void handleAddHostname() }}
          />
          <button disabled={hnPattern.trim() === ''} onClick={() => void handleAddHostname()}>{t('common.add')}</button>
        </div>
        {hnError && <div className="gateway-form-error" role="alert">{hnError}</div>}
      </div>

      <div className="gateway-card">
        <h3>{t('gateway.activeRulesTitle')}</h3>
        <table className="gateway-rules-table">
          <thead>
            <tr><th>{t('gateway.table.network')}</th><th>{t('gateway.table.action')}</th><th>{t('gateway.table.source')}</th><th>{t('gateway.table.reason')}</th><th>{t('gateway.table.expires')}</th><th aria-label={t('gateway.table.actions')}></th></tr>
          </thead>
          <tbody>
            {rules.map(rule => (
              <tr key={rule.id}>
                <td>{rule.network}</td>
                <td>{rule.action}</td>
                <td><span className={`source-badge ${rule.source}`}>{rule.source}</span></td>
                <td>{rule.description}</td>
                <td>{rule.expires_at ? new Date(rule.expires_at * 1000).toLocaleTimeString() : t('gateway.permanent')}</td>
                <td><button onClick={() => handleRemove(rule.id)} aria-label={`${t('common.remove')} ${rule.network}`}>{t('common.remove')}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="gateway-card">
        <h3>{t('gateway.fingerprintTitle')}</h3>
        {fingerprint ? (
          <ul className="gateway-fingerprint-list">
            <li>{t('gateway.ttlNormalization')}: {fingerprint.ttl_normalize ?? t('gateway.off')}</li>
            <li>{t('gateway.macRandomization')}: {fingerprint.mac_randomization ? t('gateway.on') : t('gateway.off')}</li>
          </ul>
        ) : (
          <span className="gateway-empty">{t('gateway.unavailable')}</span>
        )}
      </div>

      <div className="gateway-card">
        <h3>{t('gateway.addManualRuleTitle')}</h3>
        <div className="gateway-add-form">
          <input placeholder={t('gateway.cidrPlaceholder')} aria-label={t('gateway.cidrPlaceholder')} value={network} onChange={e => setNetwork(e.target.value)} />
          <input placeholder={t('gateway.ttlPlaceholder')} aria-label={t('gateway.ttlPlaceholder')} value={ttl} onChange={e => setTtl(e.target.value)} type="number" min="1" />
          <input placeholder={t('gateway.reasonPlaceholder')} aria-label={t('gateway.reasonPlaceholder')} value={reason} onChange={e => setReason(e.target.value)} />
          <button disabled={!canSubmit} onClick={handleAddRule}>{t('gateway.addRuleButton')}</button>
        </div>
        {formError && <div className="gateway-form-error" role="alert">{formError}</div>}
      </div>

      <div className="gateway-card">
        <h3>{t('gateway.auditLogTitle')} <span className={`chain-badge ${chainVerified ? 'verified' : 'broken'}`}>{chainVerified ? t('gateway.verified') : t('gateway.broken')}</span></h3>
        <table className="gateway-audit-table">
          <thead>
            <tr><th>{t('gateway.table.time')}</th><th>{t('gateway.table.decision')}</th><th>{t('gateway.table.target')}</th><th>{t('gateway.table.reason')}</th></tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.id}>
                <td>{new Date(row.timestamp / 1_000_000).toLocaleTimeString()}</td>
                <td>{row.decision}</td>
                <td>{row.dst_ip}</td>
                <td>{row.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </main>
  )
}
