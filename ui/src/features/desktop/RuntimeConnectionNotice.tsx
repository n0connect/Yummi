import { useCallback, useEffect, useState } from 'react'
import { apiURL, isDesktopShell } from '../../shared/api/runtime'
import { t } from '../../shared/i18n/strings'

type ConnectionState = 'checking' | 'offline' | 'connected'

// The desktop console intentionally has no shell/Docker capability. This
// visible state makes that boundary legible: the operator starts the audited
// runtime separately, while this app only talks to the loopback API.
export function RuntimeConnectionNotice(): JSX.Element | null {
  const [state, setState] = useState<ConnectionState>('checking')

  const check = useCallback(async (showChecking = true) => {
    if (showChecking) setState('checking')
    try {
      const response = await fetch(apiURL('/healthz'))
      setState(response.ok ? 'connected' : 'offline')
    } catch {
      setState('offline')
    }
  }, [])

  useEffect(() => {
    if (!isDesktopShell) return
    void check()
    const timer = window.setInterval(() => { void check(false) }, 10_000)
    return () => window.clearInterval(timer)
  }, [check])

  if (!isDesktopShell || state === 'connected') return null

  return (
    <aside className="runtime-notice" role="alert" aria-live="polite">
      <span className={`dot ${state === 'checking' ? 'dot-warn pulse' : 'dot-err'}`} />
      <div className="runtime-notice-copy">
        <strong>{state === 'checking' ? t('runtimeNotice.checking') : t('runtimeNotice.disconnected')}</strong>
        {state === 'offline' && <span>{t('runtimeNotice.hintBefore')}<code>make start</code>{t('runtimeNotice.hintAfter')}</span>}
      </div>
      {state === 'offline' && <button className="runtime-retry" onClick={() => void check()}>{t('runtimeNotice.retry')}</button>}
    </aside>
  )
}
