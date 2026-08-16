import { useState } from 'react'
import type { ActivityEvent, ActivityKind } from '../../entities/types'
import { presentActivity } from '../../shared/lib/activityPresentation'
import { resolveSessionID, useTraceData } from '../../features/trace/useTraceData'
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

// Both severity and outcome are typed `<literal> | string` (entities/types.ts)
// so a future/unrecognized backend value type-checks — but that means it must
// never be interpolated directly into a className. Closed-set lookup with a
// safe default instead.
function severityClass(severity: string): string {
  switch (severity) {
    case 'info':     return 'trace-finding sev-info'
    case 'low':       return 'trace-finding sev-low'
    case 'medium':    return 'trace-finding sev-medium'
    case 'high':      return 'trace-finding sev-high'
    case 'critical':  return 'trace-finding sev-critical'
    default:          return 'trace-finding sev-unknown'
  }
}

function outcomeClass(outcome: string): string {
  switch (outcome) {
    case 'worked':       return 'trace-outcome out-worked'
    case 'failed':        return 'trace-outcome out-failed'
    case 'inconclusive':  return 'trace-outcome out-inconclusive'
    default:               return 'trace-outcome out-unknown'
  }
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
  const { activity, findings, tactics, context, skills, roles, refresh } = useTraceData(sessionID)
  const { pending: pendingSkills, approve: approveSkill, reject: rejectSkill } = usePendingSkills()
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

        {/* Findings */}
        <Section title={t('trace.sectionFindings')} count={findings.length}>
          {findings.map(f => (
            <div key={f.id} className={severityClass(f.severity)}>
              <span className="trace-sev">{f.severity}</span>
              <span className="trace-finding-title">{f.title}</span>
              {f.source && <span className="trace-src">{f.source}</span>}
              {f.description && <div className="trace-finding-desc">{f.description}</div>}
            </div>
          ))}
        </Section>

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
            registry, reject() just marks the row. */}
        <Section title={t('trace.sectionPendingSkills')} count={pendingSkills.length}>
          {skillError && <div className="trace-skill-error" role="alert">{skillError}</div>}
          {pendingSkills.map(p => (
            <div key={p.id} className="trace-skill trace-pending-skill">
              <div className="trace-pending-skill-head">
                <span className="trace-skill-name">{p.name}</span>
                <div className="trace-pending-skill-actions">
                  <button className="small-button" onClick={() => handleApproveSkill(p.id)}>{t('trace.approve')}</button>
                  <button className="small-button" onClick={() => handleRejectSkill(p.id)}>{t('common.reject')}</button>
                </div>
              </div>
              <details>
                <summary>{t('trace.draftYamlSummary')}</summary>
                <pre className="trace-context-val">{p.draft_yaml}</pre>
              </details>
            </div>
          ))}
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
