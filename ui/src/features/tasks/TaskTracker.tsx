import type {
  ActivityEvent,
  AgentTask,
  PlanStep,
  TaskStatus
} from '../../entities/types'
import { presentActivity } from '../../shared/lib/activityPresentation'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import './TaskTracker.css'

const STATUS_LABEL: Record<string, string> = {
  planning: 'Planlanıyor',
  planned: 'Plan hazır',
  running: 'Çalışıyor',
  awaiting_approval: 'Onay bekliyor',
  paused: 'Duraklatıldı',
  completed: 'Tamamlandı',
  failed: 'Başarısız',
  stopped: 'Durduruldu',
  interrupted: 'Kesildi'
}

const isTerminal = (s: TaskStatus) =>
  ['completed', 'failed', 'stopped', 'interrupted'].includes(s)

// A finding's verdict is computed server-side against the plan step's real
// recorded output (see FindingRecord in entities/types.ts) — never a bare
// model assertion, so the badge text says so rather than echoing "found".
const VERDICT_LABEL: Record<string, string> = {
  verified: 'DOĞRULANDI',
  unsupported: 'DESTEKSİZ',
  candidate: 'İDDİA'
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

function PlanTimeline({ plan }: { plan: PlanStep[] }): JSX.Element {
  return (
    <ol className="tt-timeline">
      {plan.map((step) => {
        const g = stepGlyph(step.status)
        return (
          <li key={step.id} className={`tt-step tt-step-${g.cls}`}>
            <span
              className={`tt-step-marker ${g.cls === 'running' ? 'pulse' : ''}`}
            >
              {g.icon}
            </span>
            <div className="tt-step-body">
              <span className="tt-step-desc">
                {sanitizeOperatorText(step.description, 'Plan adımı')}
              </span>
              {step.status === 'failed' ? (
                <span className="tt-step-error">Adım tamamlanamadı.</span>
              ) : (
                step.result && (
                  <span className="tt-step-result">Sonuç kaydedildi.</span>
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
    <section className="tt-live-feed" aria-label="Canlı görev akışı">
      <div className="tt-live-head">
        <span>Canlı akış</span>
        <span>{activities.length} olay</span>
      </div>
      <ol className="tt-live-list">
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
  liveTask: t,
  onClose,
  onApprove,
  onStop,
  activities = [],
  subTask,
  onApproveSub
}: Props): JSX.Element {
  const pct =
    t.step_budget > 0 ? Math.min(100, (t.steps_used / t.step_budget) * 100) : 0
  const terminal = isTerminal(t.status)
  const recordTitles = new Set(
    (t.finding_records ?? []).map((record) =>
      sanitizeOperatorText(record.title).trim().toLocaleLowerCase('tr-TR')
    )
  )
  const unmatchedFindings = (t.findings ?? []).filter(
    (finding) =>
      !recordTitles.has(
        sanitizeOperatorText(finding).trim().toLocaleLowerCase('tr-TR')
      )
  )
  const hasFindings =
    (t.finding_records?.length ?? 0) > 0 || unmatchedFindings.length > 0

  return (
    <div className={`tt-card tt-status-${t.status}`}>
      <header className="tt-header">
        <div className="tt-header-main">
          <span className={`tt-pill tt-pill-${t.status}`}>
            {!terminal && <span className="tt-pulse">●</span>}
            {STATUS_LABEL[t.status] ?? t.status}
          </span>
          <span className="tt-goal">
            {sanitizeOperatorText(t.goal, 'Görev')}
          </span>
        </div>
        {terminal && (
          <button className="tt-x" onClick={onClose} title="Kapat">
            ✕
          </button>
        )}
      </header>

      {/* Progress bar */}
      <div className="tt-progress">
        <div className="tt-progress-bar" style={{ width: `${pct}%` }} />
        <span className="tt-progress-label">
          {t.steps_used}/{t.step_budget} adım
        </span>
      </div>

      {/* Plan timeline */}
      {t.plan && t.plan.length > 0 && <PlanTimeline plan={t.plan} />}

      <LiveActivityFeed activities={activities} />

      {/* Awaiting approval — inline gate */}
      {t.status === 'awaiting_approval' && t.pending_proposal_id && (
        <div className="tt-gate">
          <div className="tt-gate-head">
            <span className={`tt-risk tt-risk-${t.pending_risk}`}>
              {t.pending_risk}
            </span>
            <span className="tt-gate-title">Komut onayı gerekiyor</span>
          </div>
          <code className="tt-gate-cmd">$ {t.pending_command}</code>
          <div className="tt-actions">
            <button
              className="tt-btn tt-btn-approve"
              onClick={() => onApprove(t.pending_proposal_id!, true)}
            >
              ✓ Onayla &amp; Çalıştır
            </button>
            <button
              className="tt-btn tt-btn-deny"
              onClick={() => onApprove(t.pending_proposal_id!, false)}
            >
              ✕ Reddet
            </button>
          </div>
        </div>
      )}

      {/* Sub-agent's own pending approval, nested under the parent it was
          delegated from — see the Props.subTask doc comment above. */}
      {subTask?.status === 'awaiting_approval' &&
        subTask.pending_proposal_id && (
          <div className="tt-gate tt-gate-nested">
            <div className="tt-gate-head">
              <span className="tt-gate-nested-badge">↳ alt-görev</span>
              <span className={`tt-risk tt-risk-${subTask.pending_risk}`}>
                {subTask.pending_risk}
              </span>
              <span className="tt-gate-title">Komut onayı gerekiyor</span>
            </div>
            <code className="tt-gate-cmd">$ {subTask.pending_command}</code>
            <div className="tt-actions">
              <button
                className="tt-btn tt-btn-approve"
                onClick={() =>
                  onApproveSub?.(subTask.pending_proposal_id!, true)
                }
              >
                ✓ Onayla &amp; Çalıştır
              </button>
              <button
                className="tt-btn tt-btn-deny"
                onClick={() =>
                  onApproveSub?.(subTask.pending_proposal_id!, false)
                }
              >
                ✕ Reddet
              </button>
            </div>
          </div>
        )}

      {/* Paused — question or error */}
      {t.status === 'paused' && (
        <div className="tt-paused">
          {t.question && (
            <p className="tt-question">
              {sanitizeOperatorText(t.question, 'Operatör girdisi gerekiyor.')}
            </p>
          )}
          {t.last_error && (
            <p className="tt-error">
              Görev devam ettirilemedi. Teknik ayrıntılar Logs ekranında.
            </p>
          )}
        </div>
      )}

      {/* Running / awaiting — stop control */}
      {(t.status === 'running' ||
        t.status === 'awaiting_approval' ||
        t.status === 'planning') && (
        <div className="tt-actions tt-actions-end">
          <button className="tt-btn tt-btn-ghost" onClick={onStop}>
            ◼ Durdur
          </button>
        </div>
      )}

      {/* Terminal — summary + findings */}
      {terminal && (
        <div className="tt-summary">
          {t.summary && (
            <p className="tt-summary-text">{sanitizeOperatorText(t.summary)}</p>
          )}
          {hasFindings && (
            <ul className="tt-findings">
              {(t.finding_records ?? []).map((r, i) => (
                <li
                  key={`record-${i}`}
                  className={`tt-finding tt-finding-${r.verdict}`}
                >
                  <span
                    className={`tt-finding-badge tt-finding-badge-${r.verdict}`}
                  >
                    {VERDICT_LABEL[r.verdict] ?? 'İDDİA'}
                  </span>
                  <span className="tt-finding-copy">
                    <span className="tt-finding-text">
                      {sanitizeOperatorText(r.title, 'Bulgu kaydedildi.')}
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
                    İDDİA
                  </span>
                  <span className="tt-finding-text">
                    {sanitizeOperatorText(f, 'Bulgu kaydedildi.')}
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
