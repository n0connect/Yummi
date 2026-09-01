import { useEffect, useRef, useState } from 'react'
import RFB from '@novnc/novnc'
import { websocketURL } from '../../shared/api/runtime'
import { t } from '../../shared/i18n/strings'
import './DesktopPanel.css'

type Status = 'connecting' | 'connected' | 'disconnected'

// DesktopPanel is Stage 2 of the browser-Linux workbench: a real XFCE
// desktop inside the sandbox, reached via noVNC (RFB.js) over orchestrator's
// /ws/desktop bridge — see docs/superpowers/specs/2026-09-01-browser-linux-
// pwnbox-design.md. Opt-in on the server side (the "desktop" compose
// profile); this panel degrades to a clear "not running" state rather than
// a stuck spinner when sandbox-desktop was never started, since that's the
// expected common case, not an error.
export function DesktopPanel(): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null)
  const rfbRef = useRef<InstanceType<typeof RFB> | null>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [unavailable, setUnavailable] = useState(false)
  // Bumped to force a fresh connect() after the operator clicks Retry.
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!containerRef.current) return
    setStatus('connecting')
    setUnavailable(false)

    const rfb = new RFB(containerRef.current, websocketURL('/ws/desktop'))
    // resizeSession (not scaleViewport): tell the SERVER to actually resize
    // the X display (TigerVNC supports this via RandR/ExtendedDesktopSize)
    // to match the browser viewport exactly, instead of CSS-scaling a fixed
    // 1600x900 desktop into whatever aspect ratio the panel happens to be —
    // the latter was live-observed leaving visible black letterbox bars on
    // any viewport that isn't exactly 16:9. This is what makes it feel like
    // a native desktop at the operator's own resolution, not a window onto
    // a fixed-size remote screen.
    rfb.scaleViewport = false
    rfb.resizeSession = true
    rfbRef.current = rfb

    const onConnect = () => setStatus('connected')
    const onDisconnect = (e: Event) => {
      setStatus('disconnected')
      // RFB's disconnect event carries { clean: boolean } — an unclean
      // disconnect on the FIRST attempt (never reached "connected") almost
      // always means orchestrator's dial to sandbox-desktop failed, i.e.
      // the desktop profile isn't running — the common, expected case this
      // panel should explain rather than just show a bare error.
      const detail = (e as CustomEvent<{ clean: boolean }>).detail
      if (!detail?.clean) setUnavailable(true)
    }
    rfb.addEventListener('connect', onConnect)
    rfb.addEventListener('disconnect', onDisconnect)

    return () => {
      rfb.removeEventListener('connect', onConnect)
      rfb.removeEventListener('disconnect', onDisconnect)
      rfb.disconnect()
      rfbRef.current = null
    }
  }, [attempt])

  return (
    <section className="panel desktop-panel">
      <div className="panel-header">
        <span className="panel-header-title">
          <span className={`dot ${status === 'connected' ? 'dot-ok' : 'dot-err'}`} aria-hidden="true" />
          {t('desktopPanel.title')}
          <span className="desktop-status-label">
            {status === 'connecting' && t('desktopPanel.connecting')}
            {status === 'connected' && t('desktopPanel.connected')}
            {status === 'disconnected' && t('desktopPanel.disconnected')}
          </span>
        </span>
      </div>
      <div className="panel-body desktop-body" style={{ padding: 0, overflow: 'hidden' }}>
        <div ref={containerRef} className="desktop-host" />
        {unavailable && (
          <div className="desktop-unavailable">
            <p className="desktop-unavailable-title">{t('desktopPanel.unavailableTitle')}</p>
            <code className="desktop-unavailable-cmd">{t('desktopPanel.unavailableBody')}</code>
            <button
              type="button"
              className="small-button"
              onClick={() => setAttempt(a => a + 1)}
            >
              {t('desktopPanel.retry')}
            </button>
          </div>
        )}
      </div>
    </section>
  )
}
