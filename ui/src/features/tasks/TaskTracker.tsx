import type { AgentTask, PlanStep, TaskStatus } from '../../entities/types'
import './TaskTracker.css'

const STATUS_LABEL: Record<string, string> = {
  planning:          'Planlanıyor',
  planned:           'Plan hazır',
  running:           'Çalışıyor',
  awaiting_approval: 'Onay bekliyor',
  paused:            'Duraklatıldı',
  completed:         'Tamamlandı',
  failed:            'Başarısız',
  stopped:           'Durduruldu',
  interrupted:       'Kesildi',
}

const isTerminal = (s: TaskStatus) =>
  ['completed', 'failed', 'stopped', 'interrupted'].includes(s)

function stepGlyph(status: string): { icon: string; cls: string } {
  switch (status) {
    case 'done':    return { icon: '✓', cls: 'done' }
    case 'running': return { icon: '◆', cls: 'running' }
    case 'failed':  return { icon: '✕', cls: 'failed' }
    case 'skipped': return { icon: '–', cls: 'skipped' }
    default:        return { icon: '○', cls: 'pending' }
  }
}

function PlanTimeline({ plan }: { plan: PlanStep[] }): JSX.Element {
  return (
    <ol className="tt-timeline">
      {plan.map(step => {
        const g = stepGlyph(step.status)
        return (
          <li key={step.id} className={`tt-step tt-step-${g.cls}`}>
            <span className={`tt-step-marker ${g.cls === 'running' ? 'pulse' : ''}`}>{g.icon}</span>
            <div className="tt-step-body">
              <span className="tt-step-desc">{step.description}</span>
              {step.status === 'failed' && step.error
                ? <span className="tt-step-error">{step.error}</span>
                : step.result && <span className="tt-step-result">{step.result}</span>}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

type Props = {
  liveTask: AgentTask
  onClose: () => void
  onApprove: (proposalID: string, approved: boolean) => void
  onStop: () => void
  // subTask is set when this task delegated to a sub-agent (via [HANDOFF])
  // and that sub-agent's own task is awaiting operator approval. Without
  // this, a sub-task's pending approval was structurally invisible — it has
  // its own task ID the UI never otherwise learns about, so the operator had
  // no way to discover or act on it (fail-closed, but stuck forever from
  // their point of view).
  subTask?: AgentTask | null
  onApproveSub?: (proposalID: string, approved: boolean) => void
}

export function TaskTracker({ liveTask: t, onClose, onApprove, onStop, subTask, onApproveSub }: Props): JSX.Element {
  const pct      = t.step_budget > 0 ? Math.min(100, (t.steps_used / t.step_budget) * 100) : 0
  const terminal = isTerminal(t.status)

  return (
    <div className={`tt-card tt-status-${t.status}`}>
      <header className="tt-header">
        <div className="tt-header-main">
          <span className={`tt-pill tt-pill-${t.status}`}>
            {!terminal && <span className="tt-pulse">●</span>}
            {STATUS_LABEL[t.status] ?? t.status}
          </span>
          <span className="tt-goal">{t.goal}</span>
        </div>
        {terminal && (
          <button className="tt-x" onClick={onClose} title="Kapat">✕</button>
        )}
      </header>

      {/* Progress bar */}
      <div className="tt-progress">
        <div className="tt-progress-bar" style={{ width: `${pct}%` }} />
        <span className="tt-progress-label">{t.steps_used}/{t.step_budget} adım</span>
      </div>

      {/* Plan timeline */}
      {t.plan && t.plan.length > 0 && <PlanTimeline plan={t.plan} />}

      {/* Awaiting approval — inline gate */}
      {t.status === 'awaiting_approval' && t.pending_proposal_id && (
        <div className="tt-gate">
          <div className="tt-gate-head">
            <span className={`tt-risk tt-risk-${t.pending_risk}`}>{t.pending_risk}</span>
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
      {subTask?.status === 'awaiting_approval' && subTask.pending_proposal_id && (
        <div className="tt-gate tt-gate-nested">
          <div className="tt-gate-head">
            <span className="tt-gate-nested-badge">↳ alt-görev</span>
            <span className={`tt-risk tt-risk-${subTask.pending_risk}`}>{subTask.pending_risk}</span>
            <span className="tt-gate-title">Komut onayı gerekiyor</span>
          </div>
          <code className="tt-gate-cmd">$ {subTask.pending_command}</code>
          <div className="tt-actions">
            <button
              className="tt-btn tt-btn-approve"
              onClick={() => onApproveSub?.(subTask.pending_proposal_id!, true)}
            >
              ✓ Onayla &amp; Çalıştır
            </button>
            <button
              className="tt-btn tt-btn-deny"
              onClick={() => onApproveSub?.(subTask.pending_proposal_id!, false)}
            >
              ✕ Reddet
            </button>
          </div>
        </div>
      )}

      {/* Paused — question or error */}
      {t.status === 'paused' && (
        <div className="tt-paused">
          {t.question   && <p className="tt-question">{t.question}</p>}
          {t.last_error && <p className="tt-error">{t.last_error}</p>}
        </div>
      )}

      {/* Running / awaiting — stop control */}
      {(t.status === 'running' || t.status === 'awaiting_approval' || t.status === 'planning') && (
        <div className="tt-actions tt-actions-end">
          <button className="tt-btn tt-btn-ghost" onClick={onStop}>
            ◼ Durdur
          </button>
        </div>
      )}

      {/* Terminal — summary + findings */}
      {terminal && (
        <div className="tt-summary">
          {t.summary && <p className="tt-summary-text">{t.summary}</p>}
          {t.findings && t.findings.length > 0 && (
            <ul className="tt-findings">
              {t.findings.map((f, i) => <li key={i}>{f}</li>)}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
