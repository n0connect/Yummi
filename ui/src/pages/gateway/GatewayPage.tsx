import { useState, useEffect } from 'react'
import { useGatewayRules, useGatewayAudit, addGatewayRule, removeGatewayRule, useEgressMode, useHostnamePolicy, type HostnameList } from '../../features/gateway/useGatewayData'
import type { GatewayEgressMode } from '../../entities/types'
import './GatewayPage.css'

const EGRESS_MODES: { mode: GatewayEgressMode; label: string; hint: string }[] = [
  { mode: 'block', label: 'Tümü Engellensin', hint: 'Sandbox dış ağa erişemez (fail-closed).' },
  { mode: 'allow', label: 'Tümüne İzin Ver', hint: 'Sandbox dış ağa serbestçe erişebilir.' },
  { mode: 'ask', label: 'Her Erişimde Sor', hint: 'Her dış erişim için operatör onayı istenir.' }
]

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
    fetch('/api/v1/gateway/fingerprint')
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
      <div className="gateway-page">
        <div className="gateway-card gateway-unreachable">Gateway unreachable.</div>
      </div>
    )
  }

  return (
    <div className="gateway-page">
      <div className="gateway-header">
        <h2 className="gateway-title">Gateway</h2>
        <span className={`gateway-status-badge ${status}`}>{status}</span>
      </div>

      <div className="gateway-card gateway-egress-card">
        <h3>Egress Mode</h3>
        <div className="gateway-egress-selector" role="group" aria-label="Egress mode">
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
            {egressStatus === 'not_configured' ? 'Gateway yapılandırılmamış.' : 'Gateway erişilemez.'}
          </p>
        )}
        {egressError && <div className="gateway-form-error">{egressError}</div>}
      </div>

      <div className="gateway-card">
        <h3>Hostname Policy</h3>
        <p className="gateway-egress-hint">
          İsim bazlı kapsam. <code>*.google.com</code> apex + tüm subdomainleri kapsar. Engel kuralı her şeyi yener;
          izin listesi doluysa whitelist gibi davranır. Buradan eklenenler geçicidir (yeniden başlatınca config dosyasından yüklenir).
        </p>
        <div className="gateway-hostname-lists">
          <div className="gateway-hostname-col">
            <span className="gateway-hostname-label gateway-hostname-allow">İzinli (allow)</span>
            {hostnamePolicy.allow.length === 0 && <span className="gateway-empty">— boş —</span>}
            {hostnamePolicy.allow.map(p => (
              <div key={`allow-${p}`} className="gateway-hostname-item">
                <code>{p}</code>
                <button onClick={() => handleRemoveHostname('allow', p)} title="Kaldır">✕</button>
              </div>
            ))}
          </div>
          <div className="gateway-hostname-col">
            <span className="gateway-hostname-label gateway-hostname-block">Engelli (block)</span>
            {hostnamePolicy.block.length === 0 && <span className="gateway-empty">— boş —</span>}
            {hostnamePolicy.block.map(p => (
              <div key={`block-${p}`} className="gateway-hostname-item">
                <code>{p}</code>
                <button onClick={() => handleRemoveHostname('block', p)} title="Kaldır">✕</button>
              </div>
            ))}
          </div>
        </div>
        <div className="gateway-add-form">
          <select value={hnList} onChange={e => setHnList(e.target.value as HostnameList)}>
            <option value="block">Engelle</option>
            <option value="allow">İzin ver</option>
          </select>
          <input
            placeholder="*.example.com veya example.com"
            value={hnPattern}
            onChange={e => setHnPattern(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') void handleAddHostname() }}
          />
          <button disabled={hnPattern.trim() === ''} onClick={() => void handleAddHostname()}>Ekle</button>
        </div>
        {hnError && <div className="gateway-form-error">{hnError}</div>}
      </div>

      <div className="gateway-card">
        <h3>Active Rules</h3>
        <table className="gateway-rules-table">
          <thead>
            <tr><th>Network</th><th>Action</th><th>Source</th><th>Reason</th><th>Expires</th><th></th></tr>
          </thead>
          <tbody>
            {rules.map(rule => (
              <tr key={rule.id}>
                <td>{rule.network}</td>
                <td>{rule.action}</td>
                <td><span className={`source-badge ${rule.source}`}>{rule.source}</span></td>
                <td>{rule.description}</td>
                <td>{rule.expires_at ? new Date(rule.expires_at * 1000).toLocaleTimeString() : 'permanent'}</td>
                <td><button onClick={() => handleRemove(rule.id)}>Remove</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="gateway-card">
        <h3>Fingerprint Masking</h3>
        {fingerprint ? (
          <ul className="gateway-fingerprint-list">
            <li>TTL normalization: {fingerprint.ttl_normalize ?? 'off'}</li>
            <li>MAC randomization: {fingerprint.mac_randomization ? 'on' : 'off'}</li>
          </ul>
        ) : (
          <span className="gateway-empty">unavailable</span>
        )}
      </div>

      <div className="gateway-card">
        <h3>Add Manual Rule</h3>
        <div className="gateway-add-form">
          <input placeholder="CIDR (e.g. 10.0.0.5/32)" value={network} onChange={e => setNetwork(e.target.value)} />
          <input placeholder="TTL seconds" value={ttl} onChange={e => setTtl(e.target.value)} type="number" min="1" />
          <input placeholder="Reason (required)" value={reason} onChange={e => setReason(e.target.value)} />
          <button disabled={!canSubmit} onClick={handleAddRule}>Add Rule</button>
        </div>
        {formError && <div className="gateway-form-error">{formError}</div>}
      </div>

      <div className="gateway-card">
        <h3>Audit Log <span className={`chain-badge ${chainVerified ? 'verified' : 'broken'}`}>{chainVerified ? '✓ verified' : '✗ broken'}</span></h3>
        <table className="gateway-audit-table">
          <thead>
            <tr><th>Time</th><th>Decision</th><th>Target</th><th>Reason</th></tr>
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
    </div>
  )
}
