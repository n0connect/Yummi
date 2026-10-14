import { useMemo, useRef, useEffect, useState } from 'react'
import type { ActivityEvent, AgentTask, ExecutionRecord } from '../../entities/types'
import { TaskTracker } from '../tasks/TaskTracker'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t, LANG } from '../../shared/i18n/strings'
import type { MessageRole } from './messageTypes'
import { ResearchSources } from './ResearchSources'

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
  onApproveSub,
  onToggleAutoApprove,
  showOperatorControls = true,
  compact = false
}: {
  task: AgentTask
  activities?: ActivityEvent[]
  onClose: () => void
  onApprove: (proposalID: string, approved: boolean) => void
  onStop: () => void
  onResume: (message: string) => void
  subTasks?: AgentTask[]
  onApproveSub?: (subTaskID: string, proposalID: string, approved: boolean) => void
  onToggleAutoApprove?: (enable: boolean) => void
  showOperatorControls?: boolean
  compact?: boolean
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
        onToggleAutoApprove={onToggleAutoApprove}
        activities={activities}
        showOperatorControls={showOperatorControls}
        compact={compact}
      />
    </div>
  )
}

const STARTER_PROMPTS = [
  t('assistant.starterPrompt.0'),
  t('assistant.starterPrompt.1'),
  t('assistant.starterPrompt.2')
]

// Two modes, not a free-text guess at what "web pentest" or "bug bounty
// recon" means: each goal string is written to hit the exact trigger
// keywords the matching persona skills key off (osint-enum: "bug bounty",
// "attack surface", "subdomain", "takeover"; web/api-pentest/js-recon/
// parameter-fuzzing/vuln-scanning: "web", "API", "BOLA", "javascript",
// "secrets", "hidden parameters", "XSS", "vulnerability scan") — so the
// composed instruction reliably pulls in the full methodology on the very
// first step, not a generic response the model has to reinterpret.
const TARGET_LAUNCHER_MODES = [
  {
    id: 'bugBounty' as const,
    label: t('assistant.targetLauncher.bugBounty'),
    hint: t('assistant.targetLauncher.bugBountyHint'),
    goal: t('assistant.targetLauncher.goal.bugBounty')
  },
  {
    id: 'webPentest' as const,
    label: t('assistant.targetLauncher.webPentest'),
    hint: t('assistant.targetLauncher.webPentestHint'),
    goal: t('assistant.targetLauncher.goal.webPentest')
  }
]

// TargetFirstLauncher is the actual "reflex" entry point: drop a target,
// pick a mode, go — one action, not composing a sentence around a
// fill-in-the-blank starter prompt. onLaunch receives the fully-composed
// goal and is expected to send it immediately (not just populate the input
// box), since minimizing clicks between "I have a target" and "Yummi is
// already working" is the entire point.
export function TargetFirstLauncher({
  onLaunch
}: {
  onLaunch: (goal: string) => void
}): JSX.Element {
  const [target, setTarget] = useState('')
  const [modeID, setModeID] = useState<(typeof TARGET_LAUNCHER_MODES)[number]['id'] | null>(null)

  const launch = () => {
    const trimmed = target.trim()
    if (!trimmed) return
    const mode = TARGET_LAUNCHER_MODES.find((m) => m.id === modeID)
    if (mode) {
      onLaunch(mode.goal.replace('{target}', trimmed))
    } else {
      onLaunch(t('assistant.targetLauncher.goal.general').replace('{target}', trimmed))
    }
  }

  return (
    <div className="target-launcher">
      <span className="target-launcher-eyebrow">{t('assistant.targetLauncher.eyebrow')}</span>
      <div className="target-launcher-modes" role="radiogroup">
        {TARGET_LAUNCHER_MODES.map((mode) => (
          <button
            key={mode.id}
            type="button"
            role="radio"
            aria-checked={modeID === mode.id}
            className={`target-launcher-mode${modeID === mode.id ? ' is-active' : ''}`}
            onClick={() => setModeID((prev) => (prev === mode.id ? null : mode.id))}
          >
            <span className="target-launcher-mode-label">{mode.label}</span>
            <span className="target-launcher-mode-hint">{mode.hint}</span>
          </button>
        ))}
      </div>
      <div className="target-launcher-row">
        <input
          className="target-launcher-input"
          type="text"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              launch()
            }
          }}
          placeholder={t('assistant.targetLauncher.placeholder')}
        />
        <button type="button" className="target-launcher-start" onClick={launch} disabled={!target.trim()}>
          {t('assistant.targetLauncher.start')}
        </button>
      </div>
    </div>
  )
}

export function EmptyWorkspace({
  onUsePrompt,
  onLaunchTarget
}: {
  onUsePrompt: (prompt: string) => void
  onLaunchTarget: (goal: string) => void
}): JSX.Element {
  return (
    <div className="chat-empty">
      <span className="chat-empty-eyebrow">{t('assistant.emptyEyebrow')}</span>
      <h1>{t('assistant.emptyTitle')}</h1>
      <p>
        {t('assistant.emptyBody')}
      </p>
      <TargetFirstLauncher onLaunch={onLaunchTarget} />
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
  onCancel,
  isOpen: isOpenProp,
  onToggle
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
  // Controlled open/close, driven by AssistantPanel's single-open-card
  // accordion (only the most recently dispatched execution stays expanded).
  // Both omitted = uncommitted standalone mode (tests, Storybook-style
  // usage): falls back to local state, defaulting open while active.
  isOpen?: boolean
  onToggle?: (open: boolean) => void
}): JSX.Element {
  const status = execution.status
  const isActive = ['queued', 'running'].includes(status)
  const [userToggled, setUserToggled] = useState<boolean | null>(null)
  const isOpen = isOpenProp ?? userToggled ?? isActive
  const toggle = () => {
    if (onToggle) onToggle(!isOpen)
    else setUserToggled(!isOpen)
  }

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
    <div
      className={`msg msg-execution execution-${status}${isOpen ? ' is-open' : ''}`}
      role="region"
      aria-expanded={isOpen}
    >
      <div
        className="execution-summary"
        role="button"
        tabIndex={0}
        onClick={toggle}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            toggle()
          }
        }}
        aria-label={`${execution.command} (${completedLabel})`}
      >
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
          {execution.rationale && (
            <span className="execution-rationale" title={execution.rationale}>
              {execution.rationale}
            </span>
          )}
          <div className="execution-cmd-row">
            <code className="execution-cmd">{execution.command}</code>
            <span className="execution-state">{completedLabel}</span>
          </div>
        </div>
      </div>
      {isOpen && (
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
            <ResearchSources output={visibleOutput} />
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
      )}
    </div>
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

function FlagChip({ flag }: { flag: string }): JSX.Element {
  const [copied, setCopied] = useState(false)
  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(flag)
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  return (
    <span
      className="task-summary-flag-chip"
      onClick={handleCopy}
      title={t('taskSummary.copyFlag')}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          handleCopy(e as unknown as React.MouseEvent)
        }
      }}
    >
      <span className="task-summary-flag-tag">{t('taskSummary.flagTag')}</span>
      <code className="task-summary-flag-code">{flag}</code>
      <span className="task-summary-copy-btn">
        {copied ? t('taskSummary.copied') : t('taskSummary.copy')}
      </span>
    </span>
  )
}

function renderFindingContent(text: string): JSX.Element {
  const clean = sanitizeOperatorText(text)
  const regex = /((?:[a-zA-Z0-9_]*flag\{[^}]+\})|(?:CVE-\d{4}-\d{4,7}))/gi
  const parts = clean.split(regex)
  if (parts.length === 1) {
    return <span className="task-summary-finding-text">{clean}</span>
  }
  return (
    <span className="task-summary-finding-text">
      {parts.map((part, i) => {
        if (/^[a-zA-Z0-9_]*flag\{[^}]+\}$/i.test(part)) {
          return <FlagChip key={i} flag={part} />
        }
        if (/^CVE-\d{4}-\d{4,7}$/i.test(part)) {
          return (
            <span key={i} className="task-summary-cve-chip">
              {part}
            </span>
          )
        }
        return <span key={i}>{part}</span>
      })}
    </span>
  )
}

export function TaskSummaryMessage({
  task,
  content,
  timestamp
}: {
  task?: AgentTask
  content: string
  timestamp: string
}): JSX.Element {
  const status = task?.status ?? 'completed'
  const isCompleted = status === 'completed'
  const isFailed = status === 'failed'
  const isStopped = status === 'stopped' || status === 'interrupted'

  let pillCls = 'tt-pill-completed'
  let pillText = `✓ ${t('taskSummary.completed')}`
  let cardStatusCls = 'status-completed'

  if (isFailed) {
    pillCls = 'tt-pill-failed'
    pillText = `✕ ${t('taskSummary.failed')}`
    cardStatusCls = 'status-failed'
  } else if (isStopped) {
    pillCls = 'tt-pill-stopped'
    pillText = `◼ ${t('taskSummary.stopped')}`
    cardStatusCls = 'status-stopped'
  }

  const summaryText = sanitizeOperatorText(task?.summary || content || '')
  const findings = task?.findings ?? []
  const findingRecords = task?.finding_records ?? []
  const hasRecords = findingRecords.length > 0
  const hasFindings = findings.length > 0 || hasRecords

  const timeFormatted = useMemo(() => {
    try {
      const d = new Date(timestamp)
      if (isNaN(d.getTime())) return ''
      return d.toLocaleTimeString(NUMBER_LOCALE, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
      })
    } catch {
      return ''
    }
  }, [timestamp])

  return (
    <div
      className={`msg-task-summary ${cardStatusCls}`}
      role="region"
      aria-label={t('taskSummary.title')}
    >
      <div className="task-summary-header">
        <div className="task-summary-title-group">
          <span className={`tt-pill ${pillCls}`}>{pillText}</span>
          <span className="task-summary-title">{t('taskSummary.title')}</span>
        </div>
        <div className="task-summary-meta">
          {task && typeof task.steps_used === 'number' && (
            <span className="task-summary-step-chip">
              {task.steps_used}/{task.step_budget || 15} {t('taskSummary.stepsUsedSuffix')}
            </span>
          )}
          {timeFormatted && <time className="task-summary-time">{timeFormatted}</time>}
          <a
            href="/trace"
            className="task-summary-trace-btn"
            title={t('taskSummary.viewInTrace')}
          >
            {t('taskSummary.traceBtn')}
          </a>
        </div>
      </div>

      <div className="task-summary-body">
        {summaryText && (
          <p className="task-summary-prose">{summaryText}</p>
        )}

        {hasFindings && (
          <div className="task-summary-findings">
            <div className="task-summary-findings-header">
              <span className="task-summary-findings-label">
                {t('taskSummary.findingsTitle')}
              </span>
              <span className="task-summary-findings-count">
                {hasRecords ? findingRecords.length : findings.length}
              </span>
            </div>
            <div className="task-summary-findings-list">
              {hasRecords
                ? findingRecords.map((r, i) => {
                    const isFlag = /flag\{/i.test(r.title)
                    return (
                      <div
                        key={r.step_id ? `${r.step_id}-${i}` : `rec-${i}`}
                        className={`task-summary-finding-row ${isFlag ? 'is-flag' : ''}`}
                      >
                        <span className={`task-summary-finding-badge tt-finding-badge-${r.verdict}`}>
                          {r.verdict.toUpperCase()}
                        </span>
                        <div className="task-summary-finding-copy">
                          {renderFindingContent(r.title)}
                          {r.reason && (
                            <span className="task-summary-finding-reason">
                              {sanitizeOperatorText(r.reason)}
                            </span>
                          )}
                        </div>
                      </div>
                    )
                  })
                : findings.map((f, i) => {
                    const isFlag = /flag\{/i.test(f)
                    return (
                      <div
                        key={`find-${i}`}
                        className={`task-summary-finding-row ${isFlag ? 'is-flag' : ''}`}
                      >
                        <span className="task-summary-finding-badge">#{i + 1}</span>
                        <div className="task-summary-finding-copy">
                          {renderFindingContent(f)}
                        </div>
                      </div>
                    )
                  })}
            </div>
          </div>
        )}

        {task?.last_error && !isCompleted && (
          <div className="task-summary-error">
            <span className="task-summary-error-badge">ERROR</span>
            <span className="task-summary-error-text">
              {sanitizeOperatorText(task.last_error)}
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
