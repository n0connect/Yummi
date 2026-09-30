import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import './TerminalPanel.css'
import { websocketURL } from '../../shared/api/runtime'
import { t } from '../../shared/i18n/strings'

type Props = {
  // compact drops the Clear button and shrinks the header/font for a small
  // embedded preview (see MiniSandboxPreview) — same WS connection, same
  // persistent tmux session as the full Bash tab (multiple simultaneous
  // `docker exec ... tmux new-session -A -s operator` attaches all mirror
  // the one session), just less chrome. Not a second, disposable terminal.
  compact?: boolean
  // Lets a compact embed (which hides its own header/status dot) still
  // surface connection state to whatever chrome wraps it, without
  // duplicating the WS logic that already tracks it here.
  onConnectedChange?: (connected: boolean) => void
}

export function TerminalPanel({ compact = false, onConnectedChange }: Props = {}): JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)

  // ── Mount xterm.js + WebSocket ────────────────────────────────────────────
  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      cursorBlink: !compact,
      fontFamily: '"Geist Mono", Menlo, Monaco, "Cascadia Code", monospace',
      fontSize: compact ? 10 : 12.5,
      lineHeight: 1.35,
      scrollback: 10000,
      theme: {
        background: '#0a0c0e',
        foreground: '#d2e4de',
        cursor: '#2dd4bf',
        cursorAccent: '#0a0c0e',
        selectionBackground: 'rgba(45, 212, 191, 0.25)',
        black: '#14181b',
        brightBlack: '#3a474c',
        green: '#30d158',
        brightGreen: '#5eead4',
        cyan: '#38bdf8',
        red: '#ff453a',
      },
    })

    const fitAddon = new FitAddon()
    const webLinksAddon = new WebLinksAddon()
    term.loadAddon(fitAddon)
    term.loadAddon(webLinksAddon)
    term.open(containerRef.current)
    fitAddon.fit()
    termRef.current = term
    fitAddonRef.current = fitAddon

    // ── WebSocket with exponential backoff reconnect ──────────────────────
    let retries = 0
    let retryTimer: number | undefined
    let ws: WebSocket

    const sendResize = (cols: number, rows: number) => {
      const s = socketRef.current
      if (s?.readyState === WebSocket.OPEN) {
        s.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    }

    const connect = () => {
      ws = new WebSocket(websocketURL('/ws/terminal'))
      // Binary frames: PTY I/O arrives as ArrayBuffer
      ws.binaryType = 'arraybuffer'
      socketRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        retries = 0
        term.write(`\r\n\x1b[32m${t('terminalPanel.connectedMsg')}\x1b[0m\r\n`)
        // Send initial terminal size so the PTY allocates correctly.
        sendResize(term.cols, term.rows)
      }

      ws.onmessage = e => {
        if (e.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(e.data))
        } else {
          term.write(e.data as string)
        }
      }

      ws.onclose = () => {
        setConnected(false)
        retries++
        const delay = Math.min(1500 * retries, 10_000)
        term.write(`\r\n\x1b[31m${t('terminalPanel.disconnectedMsgBefore')}${delay / 1000}${t('terminalPanel.disconnectedMsgAfter')}\x1b[0m\r\n`)
        retryTimer = window.setTimeout(connect, delay)
      }

      ws.onerror = () => {
        term.write(`\r\n\x1b[31m${t('terminalPanel.errorMsg')}\x1b[0m\r\n`)
      }
    }

    // Every keystroke / paste goes directly to the PTY as binary bytes.
    // This replaces the old <input>+submit model — xterm handles echoing.
    const dataDispose = term.onData(data => {
      const s = socketRef.current
      if (s?.readyState === WebSocket.OPEN) {
        s.send(new TextEncoder().encode(data))
      }
    })

    connect()

    // ── Resize observer — refit and notify backend ────────────────────────
    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit()
      sendResize(term.cols, term.rows)
    })
    resizeObserver.observe(containerRef.current)

    return () => {
      if (retryTimer) clearTimeout(retryTimer)
      dataDispose.dispose()
      resizeObserver.disconnect()
      socketRef.current?.close()
      term.dispose()
    }
  }, [])

  useEffect(() => {
    onConnectedChange?.(connected)
  }, [connected, onConnectedChange])

  return (
    <section className={`panel ${compact ? 'terminal-panel-compact' : ''}`}>
      {!compact && (
        <div className="panel-header">
          <span className="panel-header-title">
            <span
              className={`dot ${connected ? 'dot-ok' : 'dot-err'}`}
              title={connected ? t('terminalPanel.connected') : t('terminalPanel.disconnected')}
              role="status"
              aria-live="polite"
              aria-label={connected ? t('terminalPanel.connected') : t('terminalPanel.disconnected')}
            />
            {t('terminalPanel.title')}
          </span>
          <span className="panel-header-actions">
            <button
              className="small-button"
              onClick={() => termRef.current?.clear()}
              title={t('terminalPanel.clearTitle')}
            >
              {t('terminalPanel.clearLabel')}
            </button>
          </span>
        </div>
      )}

      {/* panel-body padding removed so xterm fills the full available area */}
      <div className="panel-body terminal-body" style={{ padding: 0, overflow: 'hidden' }}>
        <div ref={containerRef} className="terminal-host" />
      </div>
    </section>
  )
}
