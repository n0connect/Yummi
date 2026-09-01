import { useState } from 'react'
import { t } from '../../shared/i18n/strings'
import { useOperatorBriefing } from './useOperatorBriefing'
import './BriefingPanel.css'

const STORAGE_SID = 'yummi:chat:sessionId'

function outcomeLabel(outcome: string): string {
  if (outcome === 'worked') return t('briefing.outcomeWorked')
  if (outcome === 'failed') return t('briefing.outcomeFailed')
  if (outcome === 'inconclusive') return t('briefing.outcomeInconclusive')
  return outcome
}

// BriefingPanel is the operator-facing companion to the manual terminal: the
// operator names a tool (and optionally what they're working on) and Yummi
// hands back its accumulated knowledge — matching playbooks, command patterns
// that worked before, findings already on record — for the operator's OWN
// use, not folded into an agent prompt. This is the visible half of the
// "operator drives, AI assists" inversion (GET /api/v1/operator/briefing).
export function BriefingPanel(): JSX.Element {
  const { data, loading, error, queried, query, clear } = useOperatorBriefing()
  const [tool, setTool] = useState('')
  const [goal, setGoal] = useState('')

  const submit = () => {
    const session = sessionStorage.getItem(STORAGE_SID) ?? undefined
    if (!tool.trim() && !goal.trim()) return
    void query({ tool: tool.trim(), goal: goal.trim(), session })
  }

  const onClear = () => {
    setTool('')
    setGoal('')
    clear()
  }

  const nothing =
    queried &&
    !loading &&
    !error &&
    data !== null &&
    data.skills.length === 0 &&
    data.tactics.length === 0 &&
    data.findings.length === 0

  return (
    <section className="briefing" aria-label={t('briefing.title')}>
      <header className="briefing-header">
        <div>
          <h2 className="briefing-title">{t('briefing.title')}</h2>
          <p className="briefing-subtitle">{t('briefing.subtitle')}</p>
        </div>
      </header>

      <div className="briefing-controls">
        <input
          className="briefing-input"
          value={tool}
          onChange={(e) => setTool(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()}
          placeholder={t('briefing.toolPlaceholder')}
          aria-label={t('briefing.toolPlaceholder')}
        />
        <input
          className="briefing-input briefing-input-goal"
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()}
          placeholder={t('briefing.goalPlaceholder')}
          aria-label={t('briefing.goalPlaceholder')}
        />
        <button
          type="button"
          className="briefing-btn briefing-btn-primary"
          onClick={submit}
          disabled={loading || (!tool.trim() && !goal.trim())}
        >
          {loading ? t('briefing.querying') : t('briefing.query')}
        </button>
        {queried && (
          <button type="button" className="briefing-btn" onClick={onClear}>
            {t('briefing.clear')}
          </button>
        )}
      </div>

      <div className="briefing-body">
        {!queried && !loading && <p className="briefing-hint">{t('briefing.hint')}</p>}
        {error && <p className="briefing-error" role="alert">{t('briefing.error')}</p>}
        {nothing && <p className="briefing-empty">{t('briefing.empty')}</p>}

        {data && !nothing && (
          <>
            {data.skills.length > 0 && (
              <div className="briefing-section">
                <h3 className="briefing-section-title">{t('briefing.skillsHeading')}</h3>
                {data.skills.map((s) => (
                  <div key={s.name} className="briefing-skill">
                    <div className="briefing-skill-head">
                      <span className="briefing-skill-name">{s.name}</span>
                      {s.tools && s.tools.length > 0 && (
                        <span className="briefing-skill-tools">{s.tools.join(' · ')}</span>
                      )}
                    </div>
                    {s.description && <p className="briefing-skill-desc">{s.description}</p>}
                    {s.knowledge && <pre className="briefing-skill-knowledge">{s.knowledge}</pre>}
                  </div>
                ))}
              </div>
            )}

            {data.tactics.length > 0 && (
              <div className="briefing-section">
                <h3 className="briefing-section-title">{t('briefing.tacticsHeading')}</h3>
                {data.tactics.map((tac) => (
                  <div key={tac.id} className={`briefing-tactic outcome-${tac.outcome}`}>
                    <code className="briefing-tactic-cmd">{tac.command_pattern}</code>
                    <span className="briefing-tactic-meta">
                      <span className={`briefing-tactic-outcome outcome-${tac.outcome}`}>
                        {outcomeLabel(tac.outcome)}
                      </span>
                      <span className="briefing-tactic-hits">
                        {tac.hit_count}
                        {t('briefing.tacticHitCount')}
                      </span>
                    </span>
                  </div>
                ))}
              </div>
            )}

            {data.findings.length > 0 && (
              <div className="briefing-section">
                <h3 className="briefing-section-title">{t('briefing.findingsHeading')}</h3>
                {data.findings.map((f) => (
                  <div key={f.id} className={`briefing-finding severity-${f.severity}`}>
                    <span className="briefing-finding-sev">{f.severity}</span>
                    <span className="briefing-finding-title">{f.title}</span>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </section>
  )
}
