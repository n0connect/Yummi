import { useState } from 'react'
import type {
  ActivityEvent,
  AgentTask,
  PlanStep,
  TaskStatus
} from '../../entities/types'
import { presentActivity } from '../../shared/lib/activityPresentation'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t } from '../../shared/i18n/strings'
import './TaskTracker.css'

const STATUS_LABEL: Record<string, string> = {
  planning: t('taskTracker.status.planning'),
  planned: t('taskTracker.status.planned'),
  running: t('taskTracker.status.running'),
  awaiting_approval: t('taskTracker.status.awaiting_approval'),
  paused: t('taskTracker.status.paused'),
  completed: t('taskTracker.status.completed'),
  failed: t('taskTracker.status.failed'),
  stopped: t('taskTracker.status.stopped'),
  interrupted: t('taskTracker.status.interrupted')
}

const isTerminal = (s: TaskStatus) =>
  ['completed', 'failed', 'stopped', 'interrupted'].includes(s)

// A finding's verdict is computed server-side against the plan step's real
// recorded output (see FindingRecord in entities/types.ts) — never a bare
// model assertion, so the badge text says so rather than echoing "found".
const VERDICT_LABEL: Record<string, string> = {
  verified: t('taskTracker.verdict.verified'),
  unsupported: t('taskTracker.verdict.unsupported'),
  candidate: t('taskTracker.verdict.candidate')
}

function stepGlyph(status: string): { icon: string; cls: string } {
  switch (status) {
    case 'done':
      return { icon: '✓', cls: 'done' }
    case 'running':
      return { icon: '◆', cls: 'running' }
    case 'failed':
      return { icon: '✕', cls: 'failed' }
    case 'skipped':
      return { icon: '–', cls: 'skipped' }
    default:
      return { icon: '○', cls: 'pending' }
  }
}

// The marker glyph is the only visual signal for done/running/pending/skipped
// steps (unlike 'failed', which also gets a text line below it) — give it a
// real accessible name instead of leaving a bare Unicode character for
// assistive tech to guess at.
const STEP_STATUS_LABEL: Record<string, string> = {
  done: t('taskTracker.stepStatus.done'),
  running: t('taskTracker.stepStatus.running'),
  failed: t('taskTracker.stepStatus.failed'),
  skipped: t('taskTracker.stepStatus.skipped'),
  pending: t('taskTracker.stepStatus.pending')
}

function PlanTimeline({ plan }: { plan: PlanStep[] }): JSX.Element {
  return (
    <ol className="tt-timeline">
      {plan.map((step) => {
        const g = stepGlyph(step.status)
        return (
          <li key={step.id} className={`tt-step tt-step-${g.cls}`}>
            <span
              className={`tt-step-marker ${g.cls === 'running' ? 'pulse' : ''}`}
              aria-label={STEP_STATUS_LABEL[g.cls] ?? g.cls}
            >
              {g.icon}
            </span>
            <div className="tt-step-body">
              <span className="tt-step-desc">
                {sanitizeOperatorText(step.description, t('taskTracker.defaultStepDesc'))}
              </span>
              {step.status === 'failed' ? (
                <span className="tt-step-error">{t('taskTracker.stepFailed')}</span>
              ) : (
                step.result && (
                  <span className="tt-step-result">{t('taskTracker.stepResultSaved')}</span>
                )
              )}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

function LiveActivityFeed({
  activities
}: {
  activities: ActivityEvent[]
}): JSX.Element | null {
  if (activities.length === 0) return null
  return (
    <section className="tt-live-feed" aria-label={t('taskTracker.liveFeedAriaLabel')}>
      <div className="tt-live-head">
        <span>{t('taskTracker.liveFeedTitle')}</span>
        <span>{activities.length} {t('taskTracker.eventCountSuffix')}</span>
      </div>
      <ol className="tt-live-list" aria-live="polite" aria-relevant="additions">
        {activities.slice(-8).map((ev) => {
          const visible = presentActivity(ev)
          return (
            <li key={ev.id} className={`tt-live-event tt-live-${ev.kind}`}>
              <span className="tt-live-dot" aria-hidden="true" />
              <div>
                <span className="tt-live-label">{visible.label}</span>
                <span className="tt-live-summary">{visible.summary}</span>
              </div>
              <time>
                {new Date(ev.timestamp).toLocaleTimeString([], {
                  hour: '2-digit',
                  minute: '2-digit',
                  second: '2-digit'
                })}
              </time>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function SubTaskTree({ tasks }: { tasks: AgentTask[] }): JSX.Element | null {
  if (tasks.length === 0) return null
  return (
    <section className="tt-task-tree" aria-labelledby="tt-task-tree-title">
      <div className="tt-task-tree-head">
        <span id="tt-task-tree-title">{t('taskTracker.taskTreeTitle')}</span>
        <span>{tasks.length} {t('taskTracker.childCountSuffix')}</span>
      </div>
      <ol>
        {tasks
          .slice()
          .sort((a, b) => (a.created_at ?? 0) - (b.created_at ?? 0))
          .map((child) => (
            <li key={child.id} className={`tt-task-node tt-task-node-${child.status}`}>
              <span className="tt-task-branch" aria-hidden="true">└</span>
              <span className="tt-task-node-copy">
                <span>{sanitizeOperatorText(child.goal, t('taskTracker.defaultGoal'))}</span>
                <span className="tt-task-node-meta">
                  {STATUS_LABEL[child.status] ?? child.status} · {child.steps_used}/{child.step_budget}
                </span>
              </span>
            </li>
          ))}
      </ol>
    </section>
  )
}

// ApprovalGate renders one pending-command approval prompt — shared by the
// task's own gate and a delegated sub-task's nested gate (Props.subTask),
// which previously duplicated this JSX identically apart from the nested
// badge and which callback the approve/deny buttons call.
function ApprovalGate({
  risk, command, onApprove, onApproveAndAutoApprove, nested
}: {
  risk?: string
  command?: string
  onApprove: (approved: boolean) => void | Promise<void>
  // Present only for the task's own (non-nested) gate — a sub-task shares
  // its parent's AutoApproveNonCritical flag server-side (same Task field
  // semantics apply to any task ID), but offering a second, separate toggle
  // per sub-task in the UI would just be confusing without adding capability.
  onApproveAndAutoApprove?: () => void | Promise<void>
  nested?: boolean
}): JSX.Element {
  return (
    <div className={`tt-gate ${nested ? 'tt-gate-nested' : ''}`} role="alert">
      <div className="tt-gate-head">
        {nested && <span className="tt-gate-nested-badge">{t('taskTracker.subTaskBadge')}</span>}
        <span className={`tt-risk tt-risk-${risk}`}>{risk}</span>
        <span className="tt-gate-title">{t('taskTracker.approvalRequired')}</span>
      </div>
      <code className="tt-gate-cmd">$ {command}</code>
      <div className="tt-actions">
        <button className="tt-btn tt-btn-approve" onClick={() => void Promise.resolve(onApprove(true)).catch(() => {})}>
          {t('taskTracker.approveAndRun')}
        </button>
        <button className="tt-btn tt-btn-deny" onClick={() => void Promise.resolve(onApprove(false)).catch(() => {})}>
          {t('taskTracker.deny')}
        </button>
        {onApproveAndAutoApprove && (
          <button className="tt-btn tt-btn-auto" onClick={() => void Promise.resolve(onApproveAndAutoApprove()).catch(() => {})}>
            {t('taskTracker.approveAndAutoApprove')}
          </button>
        )}
      </div>
    </div>
  )
}

type Props = {
  liveTask: AgentTask
  onClose: () => void
  onApprove: (proposalID: string, approved: boolean) => void | Promise<void>
  onStop: () => void
  // Resumes a task paused at the ask_operator node, carrying the operator's
  // free-text answer (POST /api/v1/tasks/{id}/start — see tasks.go). An
  // empty message is a valid plain resume.
  onResume: (message: string) => void
  activities?: ActivityEvent[]
  // subTasks holds every currently-live sub-agent task delegated (via
  // [HANDOFF]) from this one. A single HANDOFF can fan out to several
  // specialists AT ONCE (MaxHandoffsPerStep, default 4) that run
  // concurrently — without tracking all of them, a sub-task's pending
  // approval was structurally invisible the moment a sibling's event
  // overwrote it: each has its own task ID the UI never otherwise learns
  // about, so the operator had no way to discover or act on it (fail-closed,
  // but stuck forever from their point of view).
  subTasks?: AgentTask[]
  onApproveSub?: (subTaskID: string, proposalID: string, approved: boolean) => void
  // Flips Task.AutoApproveNonCritical (POST /api/v1/tasks/{id}/auto-approve)
  // — a per-task escape from ask_permission mode for below-high risk only.
  // Omitted entirely (rather than a no-op) when the caller can't wire it, so
  // ApprovalGate's third button and the header badge/toggle both just don't
  // render instead of doing nothing on click.
  onToggleAutoApprove?: (enable: boolean) => void | Promise<void>
  showOperatorControls?: boolean
  attentionOnly?: boolean
  compact?: boolean
  busy?: boolean
}

export function TaskTracker({
  liveTask: task,
  onClose,
  onApprove,
  onStop,
  onResume,
  activities = [],
  subTasks = [],
  onApproveSub,
  onToggleAutoApprove,
  showOperatorControls = true,
  attentionOnly = false,
  compact = false,
  busy = false
}: Props): JSX.Element {
  const [replyText, setReplyText] = useState('')
  const pct =
    task.step_budget > 0 ? Math.min(100, (task.steps_used / task.step_budget) * 100) : 0

  // The Eino backend pauses with this exact, untranslated prefix (see
  // orchestrator's eino/budget.go budgetPausePrefix) when a task's step
  // budget runs out mid-goal — distinct from an ask_operator pause, which can
  // ask about anything. A free-text box for "type a number, or the word
  // stop" is real friction for something that's really a 4-way choice, and a
  // typo here silently falls back to the backend's default grant (10) with no
  // feedback — operator-observed confusion mistook this for the agent
  // "restarting" rather than a budget decision. One-click buttons make the
  // decision explicit and remove the typo path entirely; the free-text input
  // still works underneath for an atypical grant amount.
  const isBudgetPause = task.status === 'paused' && (task.question ?? '').startsWith('Step budget exhausted')
  const terminal = isTerminal(task.status)
  const recordTitles = new Set(
    (task.finding_records ?? []).map((record) =>
      sanitizeOperatorText(record.title).trim().toLowerCase()
    )
  )
  const unmatchedFindings = (task.findings ?? []).filter(
    (finding) =>
      !recordTitles.has(
        sanitizeOperatorText(finding).trim().toLowerCase()
      )
  )
  const hasFindings =
    (task.finding_records?.length ?? 0) > 0 || unmatchedFindings.length > 0

  return (
    <div className={`tt-card tt-status-${task.status} ${attentionOnly ? 'tt-attention-card' : ''}`} aria-busy={busy}>
      {!attentionOnly && <header className="tt-header">
        <div className="tt-header-main">
          <span className={`tt-pill tt-pill-${task.status}`} role="status" aria-live="polite">
            {!terminal && <span className="tt-pulse" aria-hidden="true">●</span>}
            {STATUS_LABEL[task.status] ?? task.status}
          </span>
          <span className="tt-goal">
            {sanitizeOperatorText(task.goal, t('taskTracker.defaultGoal'))}
          </span>
        </div>
        {terminal && (
          <button className="tt-x" onClick={onClose} title={t('taskTracker.closeTitle')} aria-label={t('taskTracker.closeTitle')}>
            ✕
          </button>
        )}
      </header>}

      {/* Auto-approve-non-critical status — only shown once it's actually on,
          since the toggle already appears as a button on the gate itself the
          first time it's relevant; this is purely "it's on, here's how to
          turn it off" for the rest of the task's run. */}
      {!attentionOnly && !terminal && task.auto_approve_non_critical && onToggleAutoApprove && (
        <div className="tt-auto-approve-badge">
          <span>{t('taskTracker.autoApproveOnBadge')}</span>
          <button className="tt-btn tt-btn-link" onClick={() => onToggleAutoApprove(false)}>
            {t('taskTracker.autoApproveTurnOff')}
          </button>
        </div>
      )}

      {/* Progress bar */}
      {!attentionOnly && <div className="tt-progress">
        <div className="tt-progress-bar" style={{ width: `${pct}%` }} />
        <span className="tt-progress-label">
          {task.steps_used}/{task.step_budget} {t('taskTracker.stepsUsedSuffix')}
        </span>
      </div>}

      {/* Plan timeline */}
      {!attentionOnly && (compact ? (
        <details className="tt-history-details">
          <summary>{t('taskTracker.liveFeedTitle')}</summary>
          {task.plan && task.plan.length > 0 && <PlanTimeline plan={task.plan} />}
          <LiveActivityFeed activities={activities} />
          <SubTaskTree tasks={subTasks} />
        </details>
      ) : <>
        {task.plan && task.plan.length > 0 && <PlanTimeline plan={task.plan} />}
        <LiveActivityFeed activities={activities} />
        <SubTaskTree tasks={subTasks} />
      </>)}

      <fieldset className="tt-operator-controls" disabled={busy}>

      {/* Awaiting approval — inline gate */}
      {showOperatorControls && task.status === 'awaiting_approval' && task.pending_proposal_id && (
        <ApprovalGate
          risk={task.pending_risk}
          command={task.pending_command}
          onApprove={(approved) => onApprove(task.pending_proposal_id!, approved)}
          onApproveAndAutoApprove={
            onToggleAutoApprove
              ? async () => {
                  const decision = onApprove(task.pending_proposal_id!, true)
                  if (decision) await decision
                  await onToggleAutoApprove(true)
                }
              : undefined
          }
        />
      )}

      {/* Every concurrently-delegated sub-agent's own pending approval,
          nested under the parent it was delegated from — see the
          Props.subTasks doc comment above. */}
      {showOperatorControls && subTasks
        .filter((sub) => sub.status === 'awaiting_approval' && sub.pending_proposal_id)
        .map((sub) => (
          <ApprovalGate
            key={sub.id}
            nested
            risk={sub.pending_risk}
            command={sub.pending_command}
            onApprove={(approved) => onApproveSub?.(sub.id, sub.pending_proposal_id!, approved)}
          />
        ))}

      {/* Paused — question or error, with an inline reply so the operator can
          actually resume the loop (POST /tasks/{id}/start carries the answer;
          without this the ask_operator node was a dead end — the task sat in
          "paused" forever no matter what the operator typed into the main
          chat box, which is a different, unrelated conversation channel). */}
      {showOperatorControls && task.status === 'paused' && (
        <div className="tt-paused" role="alert">
          {task.question && (
            <p className="tt-question">
              {sanitizeOperatorText(task.question, t('taskTracker.defaultQuestion'))}
            </p>
          )}
          {task.last_error && (
            <p className="tt-error">
              {t('taskTracker.taskFailedNote')}
            </p>
          )}
          {isBudgetPause && (
            <div className="tt-budget-quick-replies" role="group" aria-label={t('taskTracker.stepBudgetQuickReplyLabel')}>
              {[10, 20, 30].map((n) => (
                <button
                  key={n}
                  type="button"
                  className="tt-btn tt-btn-budget"
                  onClick={() => onResume(String(n))}
                >
                  +{n}
                </button>
              ))}
              <button type="button" className="tt-btn tt-btn-ghost" onClick={onStop}>
                {t('taskTracker.stop')}
              </button>
            </div>
          )}
          <div className="tt-resume">
            <input
              className="tt-resume-input"
              value={replyText}
              onChange={(e) => setReplyText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  onResume(replyText.trim())
                  setReplyText('')
                }
              }}
              placeholder={
                isBudgetPause
                  ? t('taskTracker.resumeStepBudgetPlaceholder')
                  : t('taskTracker.resumeReplyPlaceholder')
              }
              aria-label={
                isBudgetPause
                  ? t('taskTracker.resumeStepBudgetPlaceholder')
                  : t('taskTracker.resumeReplyPlaceholder')
              }
            />
            <div className="tt-actions">
              <button
                className="tt-btn tt-btn-approve"
                onClick={() => {
                  onResume(replyText.trim())
                  setReplyText('')
                }}
              >
                {t('taskTracker.resume')}
              </button>
              <button className="tt-btn tt-btn-ghost" onClick={onStop}>
                {t('taskTracker.abandon')}
              </button>
            </div>
          </div>
        </div>
      )}
      </fieldset>

      {/* Running / awaiting — stop control */}
      {showOperatorControls && !attentionOnly && (task.status === 'running' ||
        task.status === 'awaiting_approval' ||
        task.status === 'planning') && (
        <div className="tt-actions tt-actions-end">
          <button className="tt-btn tt-btn-ghost" onClick={onStop}>
            {t('taskTracker.stop')}
          </button>
        </div>
      )}

      {/* Terminal — summary + findings */}
      {!attentionOnly && terminal && (
        <div className="tt-summary">
          {task.summary && (
            <p className="tt-summary-text">{sanitizeOperatorText(task.summary)}</p>
          )}
          {hasFindings && (
            <ul className="tt-findings">
              {(task.finding_records ?? []).map((r, i) => (
                <li
                  key={`record-${i}`}
                  className={`tt-finding tt-finding-${r.verdict}`}
                >
                  <span
                    className={`tt-finding-badge tt-finding-badge-${r.verdict}`}
                  >
                    {VERDICT_LABEL[r.verdict] ?? t('taskTracker.verdict.candidate')}
                  </span>
                  <span className="tt-finding-copy">
                    <span className="tt-finding-text">
                      {sanitizeOperatorText(r.title, t('taskTracker.defaultFindingRecorded'))}
                    </span>
                    {r.reason && (
                      <span className="tt-finding-reason">
                        {sanitizeOperatorText(r.reason)}
                      </span>
                    )}
                  </span>
                </li>
              ))}
              {unmatchedFindings.map((f, i) => (
                <li
                  key={`legacy-${i}`}
                  className="tt-finding tt-finding-candidate"
                >
                  <span className="tt-finding-badge tt-finding-badge-candidate">
                    {t('taskTracker.verdict.candidate')}
                  </span>
                  <span className="tt-finding-text">
                    {sanitizeOperatorText(f, t('taskTracker.defaultFindingRecorded'))}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
