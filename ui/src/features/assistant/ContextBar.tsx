import { useEffect, useRef, useState } from 'react'
import { t } from '../../shared/i18n/strings'

// Mirrors orchestrator/internal/agent.ApprovalMode — exactly two modes.
export type ApprovalMode = 'ask_permission' | 'auto'

const MODE_LABEL: Record<ApprovalMode, string> = {
  ask_permission: t('contextBar.mode.ask_permission'),
  auto: t('contextBar.mode.auto'),
}

// A deliberately approximate visual budget. Model/backend identity belongs in
// Health rather than the conversation chrome.
const DEFAULT_CTX = 32768

type Props = {
  /** All message text concatenated (for rough token estimation). */
  charCount: number
  messageCount: number
  approvalMode: ApprovalMode
  onApprovalModeChange: (mode: ApprovalMode) => void
}

export function ContextBar({
  charCount, messageCount, approvalMode, onApprovalModeChange,
}: Props): JSX.Element {
  const [modeOpen, setModeOpen] = useState(false)
  const modeRef = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    if (!modeOpen) return
    const onOutside = (e: MouseEvent) => {
      if (modeRef.current && !modeRef.current.contains(e.target as Node)) setModeOpen(false)
    }
    document.addEventListener('mousedown', onOutside)
    return () => document.removeEventListener('mousedown', onOutside)
  }, [modeOpen])

  // Escape closes the dropdown, matching SessionRail's established pattern
  // for dismissible overlays elsewhere in the app.
  useEffect(() => {
    if (!modeOpen) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setModeOpen(false) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [modeOpen])

  const pickMode = (mode: ApprovalMode) => {
    onApprovalModeChange(mode)
    setModeOpen(false)
  }

  // ≈4 chars per token (BPE average; good enough for a visual indicator)
  const estimatedTokens = Math.max(0, Math.round(charCount / 4))
  const ctxWindow       = DEFAULT_CTX
  const fillPct         = Math.min(100, (estimatedTokens / ctxWindow) * 100)

  const fillColor =
    fillPct > 85 ? 'var(--red)' :
    fillPct > 65 ? 'var(--orange)' :
    'var(--accent)'

  return (
    <div className="ctx-bar">
      <div className="ctx-bar-left">
        <span className="ctx-chip ctx-chip-mode" ref={modeRef}>
          <span className="ctx-label">{t('contextBar.permissionLabel')}</span>
          <button
            type="button"
            className="ctx-mode-trigger"
            onClick={() => setModeOpen(o => !o)}
            aria-haspopup="listbox"
            aria-expanded={modeOpen}
          >
            <span className="ctx-value ctx-mono">{MODE_LABEL[approvalMode]}</span>
            <span className="ctx-mode-caret">▾</span>
          </button>
          {modeOpen && (
            <div className="ctx-mode-menu" role="listbox">
              {(['ask_permission', 'auto'] as const).map(m => (
                <button
                  key={m}
                  type="button"
                  role="option"
                  aria-selected={approvalMode === m}
                  className={`ctx-mode-option ${approvalMode === m ? 'active' : ''}`}
                  onClick={() => pickMode(m)}
                >
                  {MODE_LABEL[m]}
                </button>
              ))}
            </div>
          )}
        </span>
        <span className="ctx-dot">·</span>
        <span className="ctx-chip">
          <span className="ctx-label">{t('contextBar.messageLabel')}</span>
          <span className="ctx-value ctx-mono">{messageCount}</span>
        </span>
      </div>

      <div className="ctx-bar-right">
        <span className="ctx-chip">
          <span className="ctx-label">{t('contextBar.contextLabel')}</span>
          <span className="ctx-value ctx-mono">
            ~{estimatedTokens >= 1000
              ? `${(estimatedTokens / 1000).toFixed(1)}k`
              : estimatedTokens
            } / {(ctxWindow / 1024).toFixed(0)}k
          </span>
        </span>
        <div
          className="ctx-track"
          title={`${t('contextBar.usageTitleBefore')}${fillPct.toFixed(0)}${t('contextBar.usageTitleAfter')}`}
          role="progressbar"
          aria-label={t('contextBar.usageBarAriaLabel')}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(fillPct)}
        >
          <div
            className="ctx-fill"
            style={{ width: `${fillPct}%`, background: fillColor }}
          />
        </div>
      </div>
    </div>
  )
}
