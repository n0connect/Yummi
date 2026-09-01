import { useMemo, useRef, useEffect } from 'react'
import type { ActivityEvent, AgentTask, ExecutionRecord } from '../../entities/types'
import { TaskTracker } from '../tasks/TaskTracker'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t, LANG } from '../../shared/i18n/strings'
import type { MessageRole } from './messageTypes'

// ─── Message row renderers ──────────────────────────────────────────────────
// Pure presentational pieces of the message thread — each takes only the
// props it needs and holds no state of its own beyond a local useMemo, so
// they can be read (and changed) independently of the stateful logic in
// AssistantPanel.tsx.

// Locale for number/date formatting, derived from the build-time UI language
// (distinct from `t()`'s string dictionary — Intl needs a real BCP-47 tag).
const NUMBER_LOCALE = LANG === 'en' ? 'en-US' : 'tr-TR'

export function TaskCardMessage({
  task,
  activities,
  onClose,
  onApprove,
  onStop,
  onResume,
  subTasks,
  onApproveSub
}: {
  task: AgentTask
  activities?: ActivityEvent[]
  onClose: () => void
  onApprove: (proposalID: string, approved: boolean) => void
  onStop: () => void
  onResume: (message: string) => void
  subTasks?: AgentTask[]
  onApproveSub?: (subTaskID: string, proposalID: string, approved: boolean) => void
}): JSX.Element {
  return (
    <div className="msg-task-card">
      <TaskTracker
        liveTask={task}
        onClose={onClose}
        onApprove={onApprove}
        onStop={onStop}
        onResume={onResume}
        subTasks={subTasks}
        onApproveSub={onApproveSub}
        activities={activities}
      />
    </div>
  )
}

const STARTER_PROMPTS = [
  t('assistant.starterPrompt.0'),
  t('assistant.starterPrompt.1'),
  t('assistant.starterPrompt.2')
]

export function EmptyWorkspace({
  onUsePrompt
}: {
  onUsePrompt: (prompt: string) => void
}): JSX.Element {
  return (
    <div className="chat-empty">
      <div className="chat-empty-mark" aria-hidden="true">
        Y
      </div>
      <span className="chat-empty-eyebrow">{t('assistant.emptyEyebrow')}</span>
      <h1>{t('assistant.emptyTitle')}</h1>
      <p>
        {t('assistant.emptyBody')}
      </p>
      <div className="chat-starters" aria-label={t('assistant.startersAriaLabel')}>
        {STARTER_PROMPTS.map((prompt) => (
          <button
            key={prompt}
            className="chat-starter"
            type="button"
            onClick={() => onUsePrompt(prompt)}
          >
            <span>{prompt}</span>
            <span aria-hidden="true">↗</span>
          </button>
        ))}
      </div>
    </div>
  )
}

// formatElapsed renders a heartbeat's elapsed_secs as "Ns" or "MdkSs" — long
// enough that a plain seconds counter would stop being readable at a glance.
function formatElapsed(secs: number): string {
  const s = t('assistant.secondsShort')
  if (secs < 60) return `${secs}${s}`
  return `${Math.floor(secs / 60)}${t('assistant.minutesShort')} ${secs % 60}${s}`
}

// Very large tool output (a full nikto/nmap -A report can run into the
// hundreds of KB) shouldn't be dumped whole into the DOM just because the
// operator expanded the row — keep both ends (the start is usually the most
// orienting part, the end is often the summary/conclusion) and note what was
// omitted, the same edge-preserving pattern operatorText.ts uses for
// oversized model text.
const MAX_DISPLAY_CHARS = 120_000

// LIVE_OUTPUT_CAP bounds the client-side buffer a running execution's
// streamed chunks accumulate into (see AssistantPanel's liveOutputs) — the
// server already caps what it will ever send (sandbox.Bridge's maxOutputBytes
// and its own streaming tap stop emitting once that cap hits), but a very
// chatty command producing many small chunks under that byte cap could still
// build a large JS string client-side without this. Kept in sync with
// MAX_DISPLAY_CHARS: the live view is truncated for display the same way the
// final output is. Unlike formatOutputForDisplay's head+tail snapshot, a live
// tail keeps only the most recent bytes — for output still growing, the
// newest lines are the relevant ones.
export const LIVE_OUTPUT_CAP = MAX_DISPLAY_CHARS

function formatOutputForDisplay(text: string): {
  display: string
  truncated: boolean
  lineCount: number
} {
  const lineCount = text.length ? text.split('\n').length : 0
  if (text.length <= MAX_DISPLAY_CHARS)
    return { display: text, truncated: false, lineCount }
  const edge = MAX_DISPLAY_CHARS / 2
  const omitted = (text.length - MAX_DISPLAY_CHARS).toLocaleString(NUMBER_LOCALE)
  return {
    display: `${text.slice(0, edge)}${t('assistant.truncatedNoticeBefore')}${omitted}${t('assistant.truncatedNoticeAfter')}${text.slice(-edge)}`,
    truncated: true,
    lineCount
  }
}

export function ExecutionMessage({
  content,
  execution,
  liveOutput,
  onCancel
}: {
  content: string
  execution: ExecutionRecord
  // Streamed output accumulated so far (WS "execution.output" events) while
  // this execution is still running — see AssistantPanel's liveOutputs.
  // undefined/empty until the first chunk arrives; always superseded by
  // execution.output once the record reaches a terminal status.
  liveOutput?: string
  // Cancels this specific execution (POST /executions/{id}/cancel) — the
  // only stop affordance a bare command dispatched outside an agent task
  // has, since the composer's Stop button is wired to activeTask and stays
  // hidden when there's no task, ever (see AssistantPanel's cancelExecution).
  onCancel?: (executionID: string) => void
}): JSX.Element {
  const status = execution.status
  const isActive = ['queued', 'running'].includes(status)
  const visibleOutput =
    execution.output || (content !== execution.error ? content : '')
  const outputView = useMemo(
    () => formatOutputForDisplay(visibleOutput),
    [visibleOutput]
  )
  const liveOutputView = useMemo(
    () => (liveOutput ? formatOutputForDisplay(liveOutput) : null),
    [liveOutput]
  )
  // Auto-follow-tail: as streamed output grows, keep the live <pre> pinned to
  // the bottom the way a real terminal (and Claude Code's tool output) does —
  // but ONLY while the operator is already at the bottom. The moment they
  // scroll up to read earlier output, stop yanking them back down; resume
  // following as soon as they scroll back to the bottom themselves. Without
  // this the operator either never sees the newest line of a long-running
  // scan, or gets fought for control of the scrollbar mid-read.
  const liveOutputRef = useRef<HTMLPreElement>(null)
  const followTailRef = useRef(true)
  useEffect(() => {
    const el = liveOutputRef.current
    if (el && followTailRef.current) {
      el.scrollTop = el.scrollHeight
    }
  }, [liveOutputView?.display])
  const onLiveScroll = (e: React.UIEvent<HTMLPreElement>) => {
    const el = e.currentTarget
    // A small slack (4px) so sub-pixel rounding at the true bottom still
    // counts as "at the bottom" and keeps following.
    followTailRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 4
  }
  const statusLabel: Record<string, string> = {
    queued: t('assistant.execStatus.queued'),
    running: t('assistant.execStatus.running'),
    awaiting_egress_approval: t('assistant.execStatus.awaiting_egress_approval'),
    awaiting_threat_approval: t('assistant.execStatus.awaiting_threat_approval'),
    completed: t('assistant.execStatus.completed'),
    failed: t('assistant.execStatus.failed'),
    canceled: t('assistant.execStatus.canceled')
  }
  const completedLabel =
    status === 'completed' && visibleOutput
      ? t('assistant.completedWithOutput')
      : (statusLabel[status] ?? status)
  return (
    <details
      className={`msg msg-execution execution-${status}`}
      open={isActive}
    >
      <summary className="execution-summary">
        <span className="execution-chevron" aria-hidden="true">›</span>
        {/* Decorative — the adjacent execution-state span already spells out
            the same status as real text, so this glyph is hidden from
            assistive tech rather than announced as a bare Unicode character. */}
        <span
          className={`execution-status ${status === 'completed' ? 'status-ok' : isActive ? 'status-live' : 'status-err'}`}
          aria-hidden="true"
        >
          {status === 'completed'
            ? '✓'
            : isActive
              ? '●'
              : status === 'awaiting_egress_approval' ||
                  status === 'awaiting_threat_approval'
                ? '!'
                : '✗'}
        </span>
        <div className="execution-heading">
          <code className="execution-cmd">{execution.command}</code>
          <span className="execution-state">{completedLabel}</span>
        </div>
      </summary>
      <div className="execution-detail">
        {isActive && (
          <div className="execution-live" aria-live="polite">
            <span className="typing-dots" aria-hidden="true">
              <span />
              <span />
              <span />
            </span>
            <span>
              {status === 'queued'
                ? t('assistant.queuedLive')
                : execution.elapsed_secs
                  ? `${t('assistant.runningWithElapsedBefore')}${formatElapsed(execution.elapsed_secs)}${t('assistant.runningWithElapsedAfter')}`
                  : t('assistant.runningNoElapsed')}
            </span>
            {onCancel && (
              <button
                type="button"
                className="btn-execution-cancel"
                title={t('assistant.cancelExecutionTitle')}
                onClick={() => onCancel(execution.id)}
              >
                {t('assistant.cancelExecutionButton')}
              </button>
            )}
          </div>
        )}
        {execution.security_banner && (
          <div className="execution-threat" role="alert">
            ⚠ {execution.security_banner}
          </div>
        )}
        {isActive && liveOutputView && (
          <div className="execution-output-wrap execution-output-wrap-live">
            <div className="execution-output-meta">
              <span className="execution-output-live-badge">
                {t('assistant.liveOutputLabel')}
              </span>
              {liveOutputView.truncated && (
                <span className="execution-output-truncated">
                  {t('assistant.truncatedBadge')}
                </span>
              )}
            </div>
            <pre
              ref={liveOutputRef}
              className="execution-output"
              onScroll={onLiveScroll}
            >{liveOutputView.display}</pre>
          </div>
        )}
        {!isActive && visibleOutput && (
          <div className="execution-output-wrap">
            <div className="execution-output-meta">
              <span>{outputView.lineCount.toLocaleString(NUMBER_LOCALE)} {t('assistant.lineCountSuffix')}</span>
              {outputView.truncated && (
                <span className="execution-output-truncated">
                  {t('assistant.truncatedBadge')}
                </span>
              )}
            </div>
            <pre className="execution-output">{outputView.display}</pre>
          </div>
        )}
        {execution.error && execution.status !== 'completed' && (
          <div className="execution-error">{execution.error}</div>
        )}
      </div>
    </details>
  )
}

export function TextMessage({
  role,
  content,
  timestamp
}: {
  role: MessageRole
  content: string
  timestamp: string
}): JSX.Element {
  const visibleContent =
    role === 'assistant' ? sanitizeOperatorText(content) : content
  return (
    <div
      className={`msg msg-${role}`}
      title={new Date(timestamp).toLocaleTimeString()}
    >
      {role === 'user' && <span className="msg-prefix">{t('assistant.userPrefix')}</span>}
      {role === 'assistant' && <span className="msg-prefix">{t('assistant.assistantPrefix')}</span>}
      {role === 'system' && <span className="msg-prefix">{t('assistant.systemPrefix')}</span>}
      <span className="msg-content" style={{ whiteSpace: 'pre-wrap' }}>
        {visibleContent}
      </span>
    </div>
  )
}

// StreamingMessage renders the in-progress assistant bubble while a chat
// call's raw display tokens are still arriving over the websocket (see
// AssistantPanel's onChatStream/streamingText). It intentionally does NOT
// run sanitizeOperatorText's think-block stripping on the raw text — the
// stream carries fragments of the model's still-forming structured JSON
// envelope (the orchestrator forces format:json), not finished prose, so
// mid-stream it can legitimately look like raw JSON syntax growing in real
// time rather than a clean sentence; that's expected here and is purely a
// perceived-progress aid — the bubble is always replaced wholesale by the
// finished, fully-sanitized reply once the REST response resolves.
export function StreamingMessage({ text }: { text: string }): JSX.Element {
  // aria-live="off": this bubble mutates on almost every token and is always
  // discarded in favor of the finished, sanitized reply — announcing every
  // partial-JSON fragment to a screen reader would be noisy and useless. The
  // outer .chat-messages region (aria-live="polite") announces the real
  // reply normally once it replaces this bubble as a genuine TextMessage.
  return (
    <div className="msg msg-assistant msg-streaming" aria-live="off">
      <span className="msg-prefix">{t('assistant.assistantPrefix')}</span>
      {text ? (
        <span className="msg-content" style={{ whiteSpace: 'pre-wrap' }}>
          {text}
        </span>
      ) : (
        <span className="typing-dots" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
      )}
    </div>
  )
}
