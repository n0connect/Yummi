import { useMemo, useState } from 'react'
import type { ActivityEvent, ActivityKind, PendingSkill } from '../../entities/types'
import { presentActivity } from '../../shared/lib/activityPresentation'
import { resolveSessionID, useTraceData } from '../../features/trace/useTraceData'
import { FindingsPanel } from '../../features/trace/FindingsPanel'
import { usePendingSkills } from '../../features/skills/usePendingSkills'
import { t } from '../../shared/i18n/strings'

const KIND_META: Record<ActivityKind, [string, string]> = {
  plan:              ['◇', t('trace.kind.plan')],
  command:           ['›', t('trace.kind.command')],
  command_result:    ['✓', t('trace.kind.command_result')],
  memory_recalled:   ['○', t('trace.kind.memory_recalled')],
  skill_applied:     ['◆', t('trace.kind.skill_applied')],
  handoff_started:   ['↗', t('trace.kind.handoff_started')],
  handoff_completed: ['↙', t('trace.kind.handoff_completed')],
  finding:           ['!', t('trace.kind.finding')],
  context_saved:     ['·', t('trace.kind.context_saved')],
  ask:               ['?', t('trace.kind.ask')],
  finish:            ['■', t('trace.kind.finish')],
}

// outcome is typed `<literal> | string` (entities/types.ts) so a
// future/unrecognized backend value type-checks — but that means it must
// never be interpolated directly into a className. Closed-set lookup with a
// safe default instead.
function outcomeClass(outcome: string): string {
  switch (outcome) {
    case 'worked':       return 'trace-outcome out-worked'
    case 'failed':        return 'trace-outcome out-failed'
    case 'inconclusive':  return 'trace-outcome out-inconclusive'
    default:               return 'trace-outcome out-unknown'
  }
}

// The trajectory a curator draft was derived from, JSON-marshaled server-side
// from skillcurator.Trajectory (orchestrator/internal/skillcurator/draft.go)
// with Go's default (capitalized, untagged) field names. Every field is
// optional here defensively — this is best-effort presentation of a raw
// string blob, not a validated wire contract, so a shape mismatch degrades
// to the raw-JSON fallback in TrajectoryView rather than throwing.
type ParsedTrajectory = {
  Goal?: string
  Summary?: string
  Observations?: string[]
  Tactics?: { tool?: string; command_pattern?: string; outcome?: string }[]
}

function parseTrajectory(raw: string): ParsedTrajectory | null {
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object') return parsed as ParsedTrajectory
    return null
  } catch {
    return null
  }
}

// TrajectoryView renders the task a skill draft was learned from — the
// "why this was proposed" context an operator needs to judge a draft, not
// just its resulting YAML. Falls back to the raw JSON blob when it doesn't
// parse into the expected shape (e.g. a future/older server payload).
function TrajectoryView({ raw }: { raw: string }): JSX.Element {
  const parsed = useMemo(() => parseTrajectory(raw), [raw])
  if (!parsed || (!parsed.Goal && !parsed.Summary && !parsed.Observations?.length && !parsed.Tactics?.length)) {
    return <pre className="trace-context-val">{raw}</pre>
  }
  return (
    <div className="trace-trajectory">
      {parsed.Goal && (
        <div className="trace-trajectory-field">
          <span className="trace-trajectory-label">{t('trace.trajectoryGoal')}</span>
          <span>{parsed.Goal}</span>
        </div>
      )}
      {parsed.Summary && (
        <div className="trace-trajectory-field">
          <span className="trace-trajectory-label">{t('trace.trajectoryOutcome')}</span>
          <span>{parsed.Summary}</span>
        </div>
      )}
      {!!parsed.Tactics?.length && (
        <div className="trace-trajectory-field">
          <span className="trace-trajectory-label">{t('trace.trajectoryTactics')}</span>
          <ul className="trace-trajectory-list">
            {parsed.Tactics.map((tac, i) => (
              <li key={i}>
                <code>{tac.command_pattern ?? '—'}</code>
                {tac.tool && <span className="trace-trajectory-tag">{tac.tool}</span>}
                {tac.outcome && <span className={outcomeClass(tac.outcome)}>{tac.outcome}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {!!parsed.Observations?.length && (
        <div className="trace-trajectory-field">
          <span className="trace-trajectory-label">{t('trace.trajectoryObservations')}</span>
          <ul className="trace-trajectory-list">
            {parsed.Observations.map((obs, i) => <li key={i}>{obs}</li>)}
          </ul>
        </div>
      )}
    </div>
  )
}

function formatEpochSecs(secs: number): string {
  return new Date(secs * 1000).toLocaleString()
}

// PendingSkillCard renders one curator-drafted skill: the operator-review
// case (actions + no resolution metadata) and the read-only audit case
// (already resolved — surfaces threat_scan_result/reject_reason/resolved_at,
// the whole point of not hiding a quarantined draft's paper trail) share this
// component, distinguished by whether onApprove/onReject are supplied.
function PendingSkillCard({ skill, onApprove, onReject }: {
  skill: PendingSkill
  onApprove?: (id: string) => void
  onReject?: (id: string) => void
}): JSX.Element {
  return (
    <div className="trace-skill trace-pending-skill">
      <div className="trace-pending-skill-head">
        <span className="trace-skill-name">{skill.name}</span>
        {onApprove && onReject ? (
          <div className="trace-pending-skill-actions">
            <button className="small-button" onClick={() => onApprove(skill.id)}>{t('trace.approve')}</button>
            <button className="small-button" onClick={() => onReject(skill.id)}>{t('common.reject')}</button>
          </div>
        ) : (
          skill.resolved_at ? (
            <span className="trace-pending-skill-resolved">
              {t('trace.rejectedSkillResolvedAt')} · {formatEpochSecs(skill.resolved_at)}
            </span>
          ) : null
        )}
      </div>
      <div className="trace-pending-skill-meta">{t('trace.pendingSkillFromTask')} {skill.task_id}</div>

      {skill.threat_scan_result && (
        <div className="execution-threat trace-pending-skill-threat" role="alert">
          {t('trace.threatScanLabel')}: {skill.threat_scan_result}
        </div>
      )}
      {skill.reject_reason && (
        <div className="trace-skill-error trace-pending-skill-reject-reason">
          {t('trace.rejectReasonLabel')}: {skill.reject_reason}
        </div>
      )}

      <details>
        <summary>{t('trace.draftYamlSummary')}</summary>
        <pre className="trace-context-val">{skill.draft_yaml}</pre>
      </details>
      <details>
        <summary>{t('trace.trajectorySummary')}</summary>
        <TrajectoryView raw={skill.trajectory} />
      </details>
    </div>
  )
}

function ActivityRow({ ev }: { ev: ActivityEvent }): JSX.Element {
  const [icon] = KIND_META[ev.kind] ?? ['•', ev.kind]
  const visible = presentActivity(ev)
  const nested = Boolean(ev.parent_task_id)
  return (
    <div className="trace-row" style={{ paddingLeft: nested ? 22 : 0 }}>
      {nested && <span className="trace-thread">└</span>}
      <span className="trace-ico">{icon}</span>
      <span className="trace-kind">{visible.label}</span>
      <span className="trace-summary">{visible.summary}</span>
    </div>
  )
}

function Section({
  title,
  count,
  children,
  live = false
}: {
  title: string
  count: number
  children: React.ReactNode
  // live sections stream new items over the agent.activity websocket (see
  // useTraceData) without any user action — mark them as a polite,
  // additions-only live region so a screen reader user notices new entries
  // arriving instead of only seeing the count badge change silently.
  live?: boolean
}): JSX.Element {
  return (
    <div className="trace-section">
      <div className="trace-section-head">
        <span className="trace-section-title">{title}</span>
        <span className="trace-section-count">{count}</span>
      </div>
      {count === 0 ? (
        <div className="trace-empty">{t('trace.emptyDash')}</div>
      ) : live ? (
        <div aria-live="polite" aria-relevant="additions">{children}</div>
      ) : (
        children
      )}
    </div>
  )
}

export function TracePage(): JSX.Element {
  const [sessionID] = useState(resolveSessionID)
  const { activity, findings, targets, tactics, context, skills, roles, refresh } = useTraceData(sessionID)
  const { pending: pendingSkills, rejected: rejectedSkills, approve: approveSkill, reject: rejectSkill } = usePendingSkills()
  const [skillError, setSkillError] = useState<string | null>(null)

  async function handleApproveSkill(id: string) {
    setSkillError(null)
    try {
      await approveSkill(id)
    } catch (err) {
      setSkillError(err instanceof Error ? err.message : 'failed to approve skill')
    }
  }
  async function handleRejectSkill(id: string) {
    setSkillError(null)
    try {
      await rejectSkill(id)
    } catch (err) {
      setSkillError(err instanceof Error ? err.message : 'failed to reject skill')
    }
  }

  return (
    <main className="trace-page">
      <div className="trace-header">
        <span className="trace-page-title">{t('trace.title')}</span>
        <span className="trace-page-sub">{t('trace.subtitle')}</span>
        <button className="small-button" onClick={refresh}>{t('trace.refresh')}</button>
      </div>

      <div className="trace-grid">
        {/* Live trace */}
        <Section title={t('trace.sectionActivity')} count={activity.length} live>
          <div className="trace-list">
            {activity.map(ev => <ActivityRow key={ev.id} ev={ev} />)}
          </div>
        </Section>

        {/* Findings — severity-color-coded, filterable/sortable; see
            features/trace/FindingsPanel.tsx. Wrapped in its own grid cell
            (not the generic Section) since it owns its filter toolbar and
            empty-state copy. */}
        <div className="trace-section trace-section-findings">
          <div className="trace-section-head">
            <span className="trace-section-title">{t('trace.sectionFindings')}</span>
            <span className="trace-section-count">{findings.length}</span>
          </div>
          <FindingsPanel findings={findings} targets={targets} />
        </div>

        {/* Procedural memory */}
        <Section title={t('trace.sectionTactics')} count={tactics.length}>
          {tactics.map(t => (
            <div key={t.id} className="trace-tactic">
              <code className="trace-cmd">{t.command_pattern}</code>
              <span className={outcomeClass(t.outcome)}>{t.outcome}</span>
              <span className="trace-hits">{t.hit_count}×</span>
            </div>
          ))}
        </Section>

        {/* Persistent context / code maps */}
        <Section title={t('trace.sectionContext')} count={context.length}>
          {context.map(c => (
            <details key={c.id} className="trace-context">
              <summary>{c.key}</summary>
              <pre className="trace-context-val">{c.value}</pre>
            </details>
          ))}
        </Section>

        {/* Skills */}
        <Section title={t('trace.sectionSkills')} count={skills.length}>
          {skills.map(s => (
            <div key={s.name} className="trace-skill">
              <span className="trace-skill-name">{s.name}</span>
              {s.description && <span className="trace-skill-desc">{s.description}</span>}
            </div>
          ))}
        </Section>

        {/* Auto-skill-creation: curator-drafted skills awaiting operator review.
            Never auto-loaded — approve() writes to disk + hot-adds to the live
            registry, reject() just marks the row. Shows the full draft YAML,
            the trajectory it was learned from, and any threat-scan/reject
            metadata so an operator can judge it without leaving this page. */}
        <Section title={t('trace.sectionPendingSkills')} count={pendingSkills.length}>
          {skillError && <div className="trace-skill-error" role="alert">{skillError}</div>}
          {pendingSkills.map(p => (
            <PendingSkillCard key={p.id} skill={p} onApprove={handleApproveSkill} onReject={handleRejectSkill} />
          ))}
        </Section>

        {/* Read-only audit trail: drafts the curator or an operator rejected
            (including auto-rejections from a failed threat scan, which never
            reach the review queue above at all) — the record of WHY a draft
            was refused, not just that it was. */}
        <Section title={t('trace.sectionRejectedSkills')} count={rejectedSkills.length}>
          {rejectedSkills.map(p => <PendingSkillCard key={p.id} skill={p} />)}
        </Section>

        {/* Sub-agent roles */}
        <Section title={t('trace.sectionRoles')} count={roles.length}>
          <div className="trace-roles">
            {roles.map(r => <span key={r} className="trace-role">{r}</span>)}
          </div>
        </Section>
      </div>
    </main>
  )
}
