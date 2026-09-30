import { useState, useEffect, useCallback } from 'react'
import type { ScopeRule, ScopeRuleKind, ScopeVerdict } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { t } from '../../shared/i18n/strings'
import './ScopeRulesPanel.css'

interface ScopeRulesPanelProps {
  sessionID: string
}

export function ScopeRulesPanel({ sessionID }: ScopeRulesPanelProps): JSX.Element {
  const [rules, setRules] = useState<ScopeRule[]>([])
  const [loading, setLoading] = useState<boolean>(true)
  const [pattern, setPattern] = useState<string>('')
  const [kind, setKind] = useState<ScopeRuleKind>('include')
  const [note, setNote] = useState<string>('')
  const [submitting, setSubmitting] = useState<boolean>(false)
  const [ruleError, setRuleError] = useState<string | null>(null)

  // Live classifier state
  const [testHost, setTestHost] = useState<string>('')
  const [verdict, setVerdict] = useState<ScopeVerdict | null>(null)
  const [classifying, setClassifying] = useState<boolean>(false)

  const loadRules = useCallback(async () => {
    try {
      const sid = sessionID || 'default'
      const data = await fetchJSON<ScopeRule[]>(`/api/v1/sessions/${encodeURIComponent(sid)}/scope-rules`)
      setRules(data ?? [])
    } catch {
      setRules([])
    } finally {
      setLoading(false)
    }
  }, [sessionID])

  useEffect(() => {
    loadRules()
  }, [loadRules])

  const handleAddRule = async (e: React.FormEvent) => {
    e.preventDefault()
    setRuleError(null)
    const trimmed = pattern.trim()
    if (!trimmed) return
    if (trimmed === '*' || trimmed === '*.') {
      setRuleError(t('trace.scopeRules.invalidPattern'))
      return
    }

    setSubmitting(true)
    try {
      const sid = sessionID || 'default'
      await fetchJSON(`/api/v1/sessions/${encodeURIComponent(sid)}/scope-rules`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          pattern: trimmed,
          kind,
          note: note.trim() || undefined,
        }),
      })
      setPattern('')
      setNote('')
      await loadRules()
    } catch (err) {
      setRuleError(err instanceof Error ? err.message : 'Failed to add rule')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDeleteRule = async (ruleID: string) => {
    try {
      const sid = sessionID || 'default'
      await fetch(`/api/v1/sessions/${encodeURIComponent(sid)}/scope-rules/${encodeURIComponent(ruleID)}`, {
        method: 'DELETE',
      })
      setRules(prev => prev.filter(r => r.id !== ruleID))
    } catch {
      // Failed to delete
    }
  }

  const handleClassify = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!testHost.trim()) return

    setClassifying(true)
    setVerdict(null)
    try {
      const sid = sessionID || 'default'
      const host = testHost.trim()
      const res = await fetchJSON<Record<string, ScopeVerdict>>(`/api/v1/sessions/${encodeURIComponent(sid)}/scope-rules/classify`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hostname: host }),
      })
      if (res && res[host]) {
        setVerdict(res[host])
      }
    } catch {
      // Classification failed
    } finally {
      setClassifying(false)
    }
  }

  return (
    <div className="scope-rules-panel">
      {/* Rules list */}
      {loading ? (
        <div className="trace-empty">{t('sessionRail.loading')}</div>
      ) : rules.length === 0 ? (
        <div className="trace-empty">{t('trace.scopeRules.empty')}</div>
      ) : (
        <div className="scope-rules-list">
          {rules.map(rule => (
            <div key={rule.id} className="scope-rule-row">
              <div className="scope-rule-left">
                <span className={`scope-rule-badge ${rule.kind === 'include' ? 'scope-rule-include' : 'scope-rule-exclude'}`}>
                  {rule.kind === 'include' ? '[IN]' : '[OUT]'}
                </span>
                <span className="scope-rule-pattern">{rule.pattern}</span>
                {rule.note && <span className="scope-rule-note">({rule.note})</span>}
              </div>
              <button
                type="button"
                className="scope-rule-delete"
                onClick={() => handleDeleteRule(rule.id)}
                aria-label={`Delete rule ${rule.pattern}`}
              >
                X
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Precedence Notice */}
      <div className="scope-precedence-notice" style={{ fontSize: '10px', color: 'var(--text-muted, #888)', fontStyle: 'italic', padding: '2px 0' }}>
        {t('trace.scopeRules.precedenceNotice')}
      </div>

      {ruleError && (
        <div className="execution-threat" role="alert" style={{ fontSize: '11px', padding: '4px 8px' }}>
          {ruleError}
        </div>
      )}

      {/* Add rule form */}
      <form className="scope-rules-form" onSubmit={handleAddRule}>
        <select
          className="scope-select"
          value={kind}
          onChange={e => setKind(e.target.value as ScopeRuleKind)}
          aria-label="Scope rule type"
        >
          <option value="include">{t('trace.scopeRules.include')}</option>
          <option value="exclude">{t('trace.scopeRules.exclude')}</option>
        </select>
        <input
          type="text"
          className="scope-input"
          placeholder={t('trace.scopeRules.patternPlaceholder')}
          value={pattern}
          onChange={e => setPattern(e.target.value)}
          required
          style={{ flex: 1, minWidth: '150px' }}
        />
        <input
          type="text"
          className="scope-input"
          placeholder={t('trace.scopeRules.notePlaceholder')}
          value={note}
          onChange={e => setNote(e.target.value)}
          style={{ flex: 1, minWidth: '150px' }}
        />
        <button
          type="submit"
          className="small-button"
          disabled={submitting || !pattern.trim()}
        >
          {t('trace.scopeRules.add')}
        </button>
      </form>

      {/* Live Classifier Box */}
      <div className="scope-classifier-box">
        <span className="scope-classifier-head">{t('trace.scopeRules.testTitle')}</span>
        <form className="scope-classifier-row" onSubmit={handleClassify}>
          <input
            type="text"
            className="scope-input"
            placeholder={t('trace.scopeRules.testPlaceholder')}
            value={testHost}
            onChange={e => setTestHost(e.target.value)}
            style={{ flex: 1 }}
          />
          <button
            type="submit"
            className="small-button"
            disabled={classifying || !testHost.trim()}
          >
            {t('trace.scopeRules.testButton')}
          </button>
          {verdict && (
            <span
              className={`scope-verdict-tag ${verdict.in_scope ? 'scope-rule-include' : 'scope-rule-exclude'}`}
              title={verdict.reason}
            >
              {verdict.in_scope ? t('trace.scopeRules.inScope') : t('trace.scopeRules.outScope')}
              {verdict.matched_rule ? ` (${verdict.matched_rule})` : ''}
              {verdict.reason ? ` - ${verdict.reason}` : ''}
            </span>
          )}
        </form>
      </div>
    </div>
  )
}
