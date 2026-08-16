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

type Props = {
  liveTask: AgentTask
  onClose: () => void
  onApprove: (proposalID: string, approved: boolean) => void
  onStop: () => void
  // Resumes a task paused at the ask_operator node, carrying the operator's
  // free-text answer (POST /api/v1/tasks/{id}/start — see tasks.go). An
  // empty message is a valid plain resume.
  onResume: (message: string) => void
  activities?: ActivityEvent[]
  // subTask is set when this task delegated to a sub-agent (via [HANDOFF])
  // and that sub-agent's own task is awaiting operator approval. Without
  // this, a sub-task's pending approval was structurally invisible — it has
  // its own task ID the UI never otherwise learns about, so the operator had
  // no way to discover or act on it (fail-closed, but stuck forever from
  // their point of view).
  subTask?: AgentTask | null
  onApproveSub?: (proposalID: string, approved: boolean) => void
}

export function TaskTracker({
  liveTask: task,
  onClose,
  onApprove,
  onStop,
  onResume,
  activities = [],
  subTask,
  onApproveSub
}: Props): JSX.Element {
  const [replyText, setReplyText] = useState('')
  const pct =
    task.step_budget > 0 ? Math.min(100, (task.steps_used / task.step_budget) * 100) : 0
  const terminal = isTerminal(task.status)
  const recordTitles = new Set(
    (task.finding_records ?? []).map((record) =>
      sanitizeOperatorText(record.title).trim().toLocaleLowerCase('tr-TR')
    )
  )
  const unmatchedFindings = (task.findings ?? []).filter(
    (finding) =>
      !recordTitles.has(
        sanitizeOperatorText(finding).trim().toLocaleLowerCase('tr-TR')
      )
  )
  const hasFindings =
    (task.finding_records?.length ?? 0) > 0 || unmatchedFindings.length > 0

  return (
    <div className={`tt-card tt-status-${task.status}`}>
      <header className="tt-header">
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
      </header>

      {/* Progress bar */}
      <div className="tt-progress">
        <div className="tt-progress-bar" style={{ width: `${pct}%` }} />
        <span className="tt-progress-label">
          {task.steps_used}/{task.step_budget} {t('taskTracker.stepsUsedSuffix')}
        </span>
      </div>

      {/* Plan timeline */}
      {task.plan && task.plan.length > 0 && <PlanTimeline plan={task.plan} />}

      <LiveActivityFeed activities={activities} />

      {/* Awaiting approval — inline gate */}
      {task.status === 'awaiting_approval' && task.pending_proposal_id && (
        <div className="tt-gate" role="alert">
          <div className="tt-gate-head">
            <span className={`tt-risk tt-risk-${task.pending_risk}`}>
              {task.pending_risk}
            </span>
            <span className="tt-gate-title">{t('taskTracker.approvalRequired')}</span>
          </div>
          <code className="tt-gate-cmd">$ {task.pending_command}</code>
          <div className="tt-actions">
            <button
              className="tt-btn tt-btn-approve"
              onClick={() => onApprove(task.pending_proposal_id!, true)}
            >
              {t('taskTracker.approveAndRun')}
            </button>
            <button
              className="tt-btn tt-btn-deny"
              onClick={() => onApprove(task.pending_proposal_id!, false)}
            >
              {t('taskTracker.deny')}
            </button>
          </div>
        </div>
      )}

      {/* Sub-agent's own pending approval, nested under the parent it was
          delegated from — see the Props.subTask doc comment above. */}
      {subTask?.status === 'awaiting_approval' &&
        subTask.pending_proposal_id && (
          <div className="tt-gate tt-gate-nested" role="alert">
            <div className="tt-gate-head">
              <span className="tt-gate-nested-badge">{t('taskTracker.subTaskBadge')}</span>
              <span className={`tt-risk tt-risk-${subTask.pending_risk}`}>
                {subTask.pending_risk}
              </span>
              <span className="tt-gate-title">{t('taskTracker.approvalRequired')}</span>
            </div>
            <code className="tt-gate-cmd">$ {subTask.pending_command}</code>
            <div className="tt-actions">
              <button
                className="tt-btn tt-btn-approve"
                onClick={() =>
                  onApproveSub?.(subTask.pending_proposal_id!, true)
                }
              >
                {t('taskTracker.approveAndRun')}
              </button>
              <button
                className="tt-btn tt-btn-deny"
                onClick={() =>
                  onApproveSub?.(subTask.pending_proposal_id!, false)
                }
              >
                {t('taskTracker.deny')}
              </button>
            </div>
          </div>
        )}

      {/* Paused — question or error, with an inline reply so the operator can
          actually resume the loop (POST /tasks/{id}/start carries the answer;
          without this the ask_operator node was a dead end — the task sat in
          "paused" forever no matter what the operator typed into the main
          chat box, which is a different, unrelated conversation channel). */}
      {task.status === 'paused' && (
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
              placeholder={t('taskTracker.resumeReplyPlaceholder')}
              aria-label={t('taskTracker.resumeReplyPlaceholder')}
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

      {/* Running / awaiting — stop control */}
      {(task.status === 'running' ||
        task.status === 'awaiting_approval' ||
        task.status === 'planning') && (
        <div className="tt-actions tt-actions-end">
          <button className="tt-btn tt-btn-ghost" onClick={onStop}>
            {t('taskTracker.stop')}
          </button>
        </div>
      )}

      {/* Terminal — summary + findings */}
      {terminal && (
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
