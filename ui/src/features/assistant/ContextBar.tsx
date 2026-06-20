import { useSystemStatus } from '../system/useSystemStatus'

// Rough context-window sizes for common models (tokens).
// Used only for the visual fill bar — not an authoritative measurement.
const MODEL_CTX: Record<string, number> = {
  'qwen3:8b':        32768,
  'llama3.2:3b':     32768,
  'llama3.1:8b':     131072,
  'llama3.3:70b':    131072,
  'deepseek-r1:8b':  65536,
}
const DEFAULT_CTX = 32768

type Props = {
  /** All message text concatenated (for rough token estimation). */
  charCount: number
  sessionID: string
  messageCount: number
  isLoading: boolean
}

export function ContextBar({ charCount, sessionID, messageCount, isLoading }: Props): JSX.Element {
  const status = useSystemStatus()
  const conductor = status?.modules.find(m => m.name === 'conductor')
  const model   = (conductor?.meta?.model   as string | undefined) ?? '—'
  const backend = (conductor?.meta?.backend as string | undefined) ?? '—'

  // ≈4 chars per token (BPE average; good enough for a visual indicator)
  const estimatedTokens = Math.max(0, Math.round(charCount / 4))
  const ctxWindow       = MODEL_CTX[model] ?? DEFAULT_CTX
  const fillPct         = Math.min(100, (estimatedTokens / ctxWindow) * 100)

  const fillColor =
    fillPct > 85 ? 'var(--red)' :
    fillPct > 65 ? 'var(--orange)' :
    'var(--accent)'

  const shortSession = sessionID === 'default' ? 'new' : sessionID.slice(0, 8)

  return (
    <div className="ctx-bar">
      <div className="ctx-bar-left">
        <span className="ctx-chip">
          <span className="ctx-label">model</span>
          <span className="ctx-value">{model}</span>
        </span>
        <span className="ctx-dot">·</span>
        <span className="ctx-chip">
          <span className="ctx-label">via</span>
          <span className="ctx-value">{backend}</span>
        </span>
        <span className="ctx-dot">·</span>
        <span className="ctx-chip">
          <span className="ctx-label">session</span>
          <span className="ctx-value ctx-mono">{shortSession}</span>
        </span>
        <span className="ctx-dot">·</span>
        <span className="ctx-chip">
          <span className="ctx-label">msgs</span>
          <span className="ctx-value ctx-mono">{messageCount}</span>
        </span>
      </div>

      <div className="ctx-bar-right">
        {isLoading && <span className="ctx-thinking">thinking</span>}
        <span className="ctx-chip">
          <span className="ctx-label">ctx</span>
          <span className="ctx-value ctx-mono">
            ~{estimatedTokens >= 1000
              ? `${(estimatedTokens / 1000).toFixed(1)}k`
              : estimatedTokens
            } / {(ctxWindow / 1024).toFixed(0)}k
          </span>
        </span>
        <div className="ctx-track" title={`~${fillPct.toFixed(0)}% context used`}>
          <div
            className="ctx-fill"
            style={{ width: `${fillPct}%`, background: fillColor }}
          />
        </div>
      </div>
    </div>
  )
}
