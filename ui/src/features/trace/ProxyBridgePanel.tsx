import { useEffect, useState } from 'react'
import type { ProxyStatus } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'

export function ProxyBridgePanel(): JSX.Element {
  const [status, setStatus] = useState<ProxyStatus | null>(null)

  useEffect(() => {
    fetchJSON<ProxyStatus>('/api/v1/proxy/status')
      .then(setStatus)
      .catch(() => setStatus(null))
  }, [])

  if (!status) {
    return <div className="trace-empty">{t('trace.emptyDash')}</div>
  }

  return (
    <div className="trace-proxy-panel" style={{ display: 'flex', flexDirection: 'column', gap: '8px', fontSize: '11px', fontFamily: 'var(--font-mono, monospace)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <span style={{ color: '#30d158', fontWeight: 'bold' }}>[OK] {t('trace.proxy.ready')}</span>
        <span style={{ color: 'var(--text-muted, #888)' }}>({status.mode})</span>
      </div>
      <div style={{ color: 'var(--text-secondary, #ccc)' }}>
        {t('trace.proxy.sources')}
      </div>
      <details style={{ marginTop: '4px' }}>
        <summary style={{ cursor: 'pointer', color: 'var(--accent, #0a84ff)' }}>{t('trace.proxy.syntaxTitle')}</summary>
        <pre className="trace-context-val" style={{ fontSize: '10px' }}>
{`# Inject live HTTP flow into Yummi ASM:
curl -X POST http://localhost:9080/api/v1/proxy/inject \\
  -H "Content-Type: application/json" \\
  -d '{"session_id":"default","method":"POST","url":"https://target.com/api/login"}'`}
        </pre>
      </details>
    </div>
  )
}
