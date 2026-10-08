import { useEffect, useRef, useState } from 'react'
import RFB from '@novnc/novnc'
import { websocketURL } from '../../shared/api/runtime'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'
import { SessionContextBadge } from '../../shared/lib/SessionContextBadge'
import './DesktopPanel.css'

type Status = 'standby' | 'connecting' | 'connected' | 'disconnected'

// DesktopPanel is the browser-Linux workbench: a real XFCE desktop inside
// the SAME sandbox container the agent execs into and the Bash tab attaches
// a tmux session on, reached via noVNC (RFB.js) over orchestrator's
// /ws/desktop bridge — see docs/superpowers/specs/2026-09-01-browser-linux-
// pwnbox-design.md. The sandbox container always runs this (no separate
// opt-in image); this panel degrades to a clear "unavailable" state rather
// than a stuck spinner only if the sandbox container itself is down.
//
// For operational security (shoulder-surfing prevention) and performance
// (no background RFB canvas rendering / WS streaming until needed), the
// desktop defaults to a locked/hidden standby state on mount and tab switch.
export function DesktopPanel(): JSX.Element {
  const panelRef = useRef<HTMLElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const rfbRef = useRef<InstanceType<typeof RFB> | null>(null)
  const [isUnlocked, setIsUnlocked] = useState(false)
  const [status, setStatus] = useState<Status>('standby')
  const [unavailable, setUnavailable] = useState(false)
  const [isFullscreen, setIsFullscreen] = useState(false)
  // Bumped to force a fresh connect() after the operator clicks Retry.
  const [attempt, setAttempt] = useState(0)
  // Host-to-sandbox file sharing (VPN configs, keys, ...): drag-and-drop or
  // the file picker, landing on the sandbox's own Desktop so it's
  // immediately visible in the GUI this panel shows. Independent of
  // isUnlocked/status — uploading is a plain host->container file copy, not
  // a VNC session, so it works even while the desktop view is hidden.
  const [isDraggingOver, setIsDraggingOver] = useState(false)
  const [uploadNote, setUploadNote] = useState<string | null>(null)
  const dragCounterRef = useRef(0)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const uploadFiles = async (files: File[]) => {
    for (const file of files) {
      const form = new FormData()
      form.append('file', file)
      try {
        const res = await fetchJSON<{ filename: string }>('/api/v1/sandbox/upload', {
          method: 'POST',
          body: form,
        })
        setUploadNote(t('desktopPanel.uploadSuccessPrefix') + res.filename)
      } catch (err) {
        setUploadNote(t('desktopPanel.uploadErrorPrefix') + (err instanceof Error ? err.message : String(err)))
      }
    }
    setTimeout(() => setUploadNote(null), 5000)
  }

  const handleDragEnter = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      dragCounterRef.current += 1
      setIsDraggingOver(true)
    }
  }
  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    e.dataTransfer.dropEffect = 'copy'
  }
  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounterRef.current -= 1
    if (dragCounterRef.current <= 0) {
      dragCounterRef.current = 0
      setIsDraggingOver(false)
    }
  }
  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounterRef.current = 0
    setIsDraggingOver(false)
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      await uploadFiles(Array.from(e.dataTransfer.files))
    }
  }

  // True fullscreen (not just CSS sizing) — the same affordance any real
  // remote-desktop client (Guacamole, an RDP viewer) offers, so this reads
  // as a native workbench rather than a web page with a video embedded in
  // it. resizeSession (above) means going fullscreen also resizes the
  // remote X display to the now-larger viewport automatically.
  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen()
    } else if (panelRef.current) {
      void panelRef.current.requestFullscreen()
    }
  }

  useEffect(() => {
    const onFsChange = () => setIsFullscreen(document.fullscreenElement === panelRef.current)
    document.addEventListener('fullscreenchange', onFsChange)
    return () => document.removeEventListener('fullscreenchange', onFsChange)
  }, [])

  useEffect(() => {
    if (!isUnlocked) {
      if (rfbRef.current) {
        rfbRef.current.disconnect()
        rfbRef.current = null
      }
      setStatus('standby')
      setUnavailable(false)
      return
    }

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
      // always means orchestrator's dial to the sandbox's VNC bridge failed,
      // i.e. the sandbox container isn't up yet — the common, expected case
      // this panel should explain rather than just show a bare error.
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
  }, [isUnlocked, attempt])

  const dotClass = !isUnlocked
    ? 'dot-dim'
    : status === 'connected'
      ? 'dot-ok'
      : status === 'connecting'
        ? 'dot-warn'
        : 'dot-err'

  return (
    <section
      ref={panelRef}
      className={`panel desktop-panel ${isFullscreen ? 'desktop-panel-fullscreen' : ''} ${isDraggingOver ? 'dragging-file' : ''}`}
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div className="panel-header">
        <span className="panel-header-title">
          <span className={`dot ${dotClass}`} aria-hidden="true" />
          {t('desktopPanel.title')}
          <span className="desktop-status-label">
            {!isUnlocked && t('desktopPanel.standby')}
            {isUnlocked && status === 'connecting' && t('desktopPanel.connecting')}
            {isUnlocked && status === 'connected' && t('desktopPanel.connected')}
            {isUnlocked && status === 'disconnected' && t('desktopPanel.disconnected')}
          </span>
        </span>
        <SessionContextBadge />
        <div className="desktop-header-actions">
          {!unavailable && (
            <>
              <input
                ref={fileInputRef}
                type="file"
                multiple
                style={{ display: 'none' }}
                onChange={(e) => {
                  if (e.target.files && e.target.files.length > 0) {
                    void uploadFiles(Array.from(e.target.files))
                  }
                  e.target.value = ''
                }}
              />
              <button
                type="button"
                className="small-button desktop-upload-btn"
                onClick={() => fileInputRef.current?.click()}
                title={t('desktopPanel.dropzoneHint')}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="17 8 12 3 7 8" />
                  <line x1="12" y1="3" x2="12" y2="15" />
                </svg>
                <span>{t('desktopPanel.uploadButton')}</span>
              </button>
            </>
          )}
          {isUnlocked && (
            <button
              type="button"
              className="small-button desktop-hide-btn"
              onClick={() => setIsUnlocked(false)}
              title={t('desktopPanel.hideDesktop')}
              aria-label={t('desktopPanel.hideDesktop')}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
                <line x1="1" y1="1" x2="23" y2="23" />
              </svg>
              <span>{t('desktopPanel.hideDesktop')}</span>
            </button>
          )}
          {status === 'connected' && (
            <button
              type="button"
              className="small-button desktop-fullscreen-btn"
              onClick={toggleFullscreen}
              title={isFullscreen ? t('desktopPanel.exitFullscreen') : t('desktopPanel.enterFullscreen')}
            >
              {isFullscreen ? t('desktopPanel.exitFullscreen') : t('desktopPanel.enterFullscreen')}
            </button>
          )}
        </div>
      </div>
      <div
        className={`panel-body desktop-body ${!isUnlocked ? 'desktop-body-locked' : ''}`}
        style={{ padding: 0, overflow: 'hidden' }}
        onClick={() => {
          if (!isUnlocked) setIsUnlocked(true)
        }}
      >
        <div ref={containerRef} className="desktop-host" />

        {isDraggingOver && (
          <div className="chat-drop-overlay" onDragLeave={handleDragLeave} onDrop={handleDrop}>
            <div className="chat-drop-card">
              <div className="chat-drop-icon" aria-hidden="true">
                <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="17 8 12 3 7 8" />
                  <line x1="12" y1="3" x2="12" y2="15" />
                </svg>
              </div>
              <h3>{t('desktopPanel.dropzoneTitle')}</h3>
              <p>{t('desktopPanel.dropzoneHint')}</p>
            </div>
          </div>
        )}

        {uploadNote && <div className="desktop-upload-note">{uploadNote}</div>}

        {!isUnlocked && (
          <div
            className="desktop-locked-overlay"
            role="button"
            tabIndex={0}
            aria-label={t('desktopPanel.clickToActivate')}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setIsUnlocked(true)
              }
            }}
          >
            <div className="desktop-locked-card">
              <div className="desktop-locked-icon" aria-hidden="true">
                <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
                  <line x1="1" y1="1" x2="23" y2="23" />
                </svg>
              </div>
              <h2 className="desktop-locked-title">{t('desktopPanel.lockedTitle')}</h2>
              <p className="desktop-locked-hint">{t('desktopPanel.lockedHint')}</p>
            </div>
          </div>
        )}

        {unavailable && isUnlocked && (
          <div className="desktop-unavailable">
            <p className="desktop-unavailable-title">{t('desktopPanel.unavailableTitle')}</p>
            <code className="desktop-unavailable-cmd">{t('desktopPanel.unavailableBody')}</code>
            <button
              type="button"
              className="small-button"
              onClick={(e) => {
                e.stopPropagation()
                setAttempt(a => a + 1)
              }}
            >
              {t('desktopPanel.retry')}
            </button>
          </div>
        )}
      </div>
    </section>
  )
}
