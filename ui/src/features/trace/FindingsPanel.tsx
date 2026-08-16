import { useMemo, useState } from 'react'
import type { Finding, Target } from '../../entities/types'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t } from '../../shared/i18n/strings'
import './FindingsPanel.css'

// Findings carry a 5-level severity (knowledge.Finding.Severity, backend
// validSeverities in orchestrator/internal/api/handlers.go), one wider than
// the 4-level risk badges elsewhere in the UI (tt-risk-*, getRiskStyle in
// AssistantPanel.tsx: low/medium/high/critical only). We follow the same
// green/yellow/orange/red mapping for the shared four and add info → cyan,
// matching the --info: var(--cyan) alias already defined in app.css.
type Severity = 'info' | 'low' | 'medium' | 'high' | 'critical'
const SEVERITY_ORDER: Severity[] = ['critical', 'high', 'medium', 'low', 'info']
const SEVERITY_WEIGHT: Record<Severity, number> = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }

type SortMode = 'newest' | 'oldest' | 'severity'

function severityKey(sev: string): Severity {
  return (SEVERITY_ORDER as string[]).includes(sev) ? (sev as Severity) : 'info'
}

// Same day → time only; otherwise a short date + time, since a session's
// findings can span multiple days of engagement.
function formatWhen(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const sameDay = d.toDateString() === new Date().toDateString()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (sameDay) return time
  return `${d.toLocaleDateString([], { day: '2-digit', month: '2-digit' })} ${time}`
}

type Props = {
  findings: Finding[]
  // Resolves a Finding.target_id to its human-readable host/IP. Optional —
  // an empty/missing list just falls back to showing raw target IDs.
  targets?: Target[]
}

// FindingsPanel is the operator-facing view of a session's structured
// findings (knowledge.Finding, from GET /api/v1/sessions/{id}/findings):
// severity-color-coded, filterable by severity/target/source, sortable, and
// keeps raw tool Evidence collapsed by default so a list of 20 findings
// doesn't turn into a wall of text.
export function FindingsPanel({ findings, targets = [] }: Props): JSX.Element {
  const [activeSeverities, setActiveSeverities] = useState<Set<Severity>>(
    () => new Set(SEVERITY_ORDER)
  )
  const [targetFilter, setTargetFilter] = useState('all')
  const [sourceFilter, setSourceFilter] = useState('all')
  const [sortMode, setSortMode] = useState<SortMode>('newest')

  const targetLabel = useMemo(() => {
    const map = new Map<string, string>()
    targets.forEach((tg) => map.set(tg.id, tg.value))
    return map
  }, [targets])

  const severityCounts = useMemo(() => {
    const counts: Record<Severity, number> = { info: 0, low: 0, medium: 0, high: 0, critical: 0 }
    findings.forEach((f) => { counts[severityKey(f.severity)] += 1 })
    return counts
  }, [findings])

  const targetOptions = useMemo(() => {
    const seen = new Map<string, string>()
    findings.forEach((f) => {
      if (!f.target_id || seen.has(f.target_id)) return
      seen.set(f.target_id, targetLabel.get(f.target_id) ?? f.target_id.slice(0, 8))
    })
    return Array.from(seen.entries())
  }, [findings, targetLabel])

  const sourceOptions = useMemo(() => {
    const seen = new Set<string>()
    findings.forEach((f) => { if (f.source) seen.add(f.source) })
    return Array.from(seen)
  }, [findings])

  const hasUnassignedTarget = useMemo(() => findings.some((f) => !f.target_id), [findings])

  function toggleSeverity(sev: Severity) {
    setActiveSeverities((prev) => {
      const next = new Set(prev)
      if (next.has(sev)) next.delete(sev)
      else next.add(sev)
      return next
    })
  }

  const filtersActive =
    activeSeverities.size !== SEVERITY_ORDER.length || targetFilter !== 'all' || sourceFilter !== 'all'

  function clearFilters() {
    setActiveSeverities(new Set(SEVERITY_ORDER))
    setTargetFilter('all')
    setSourceFilter('all')
  }

  const visible = useMemo(() => {
    const filtered = findings.filter((f) => {
      if (!activeSeverities.has(severityKey(f.severity))) return false
      if (targetFilter === 'unassigned' && f.target_id) return false
      if (targetFilter !== 'all' && targetFilter !== 'unassigned' && f.target_id !== targetFilter) return false
      if (sourceFilter !== 'all' && (f.source ?? '') !== sourceFilter) return false
      return true
    })
    const sorted = [...filtered]
    if (sortMode === 'severity') {
      sorted.sort(
        (a, b) =>
          SEVERITY_WEIGHT[severityKey(b.severity)] - SEVERITY_WEIGHT[severityKey(a.severity)] ||
          (b.created_at ?? 0) - (a.created_at ?? 0)
      )
    } else if (sortMode === 'oldest') {
      sorted.sort((a, b) => (a.created_at ?? 0) - (b.created_at ?? 0))
    } else {
      sorted.sort((a, b) => (b.created_at ?? 0) - (a.created_at ?? 0))
    }
    return sorted
  }, [findings, activeSeverities, targetFilter, sourceFilter, sortMode])

  return (
    <div className="findings-panel">
      <div className="findings-toolbar">
        <div className="findings-chips" role="group" aria-label={t('trace.sectionFindings')}>
          {SEVERITY_ORDER.map((sev) => (
            <button
              key={sev}
              type="button"
              className={`findings-chip findings-chip-${sev}${activeSeverities.has(sev) ? ' active' : ''}`}
              aria-pressed={activeSeverities.has(sev)}
              onClick={() => toggleSeverity(sev)}
            >
              {sev}
              <span className="findings-chip-count">{severityCounts[sev]}</span>
            </button>
          ))}
        </div>

        <div className="findings-selects">
          {targetOptions.length > 0 && (
            <label className="findings-select-label">
              {t('trace.findings.targetLabel')}
              <select value={targetFilter} onChange={(e) => setTargetFilter(e.target.value)}>
                <option value="all">{t('trace.findings.targetAll')}</option>
                {targetOptions.map(([id, label]) => (
                  <option key={id} value={id}>{label}</option>
                ))}
                {hasUnassignedTarget && (
                  <option value="unassigned">{t('trace.findings.targetUnassigned')}</option>
                )}
              </select>
            </label>
          )}

          {sourceOptions.length > 1 && (
            <label className="findings-select-label">
              {t('trace.findings.sourceLabel')}
              <select value={sourceFilter} onChange={(e) => setSourceFilter(e.target.value)}>
                <option value="all">{t('trace.findings.sourceAll')}</option>
                {sourceOptions.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </label>
          )}

          <label className="findings-select-label">
            {t('trace.findings.sortLabel')}
            <select value={sortMode} onChange={(e) => setSortMode(e.target.value as SortMode)}>
              <option value="newest">{t('trace.findings.sortNewest')}</option>
              <option value="oldest">{t('trace.findings.sortOldest')}</option>
              <option value="severity">{t('trace.findings.sortSeverity')}</option>
            </select>
          </label>

          {filtersActive && (
            <button type="button" className="findings-clear" onClick={clearFilters}>
              {t('trace.findings.clearFilters')}
            </button>
          )}
        </div>
      </div>

      {findings.length === 0 ? (
        <div className="trace-empty">{t('trace.emptyDash')}</div>
      ) : visible.length === 0 ? (
        <div className="trace-empty">{t('trace.findings.noMatch')}</div>
      ) : (
        <ul className="findings-list">
          {visible.map((f) => (
            <FindingRow key={f.id} finding={f} targetLabel={f.target_id ? targetLabel.get(f.target_id) : undefined} />
          ))}
        </ul>
      )}
    </div>
  )
}

function FindingRow({ finding, targetLabel }: { finding: Finding; targetLabel?: string }): JSX.Element {
  const sev = severityKey(finding.severity)
  const title = sanitizeOperatorText(finding.title, finding.title)
  const description = finding.description ? sanitizeOperatorText(finding.description) : ''
  const evidence = finding.evidence ? sanitizeOperatorText(finding.evidence) : ''

  return (
    <li className={`findings-row findings-row-${sev}`}>
      <div className="findings-row-head">
        <span className={`findings-sev-badge findings-sev-badge-${sev}`}>{finding.severity}</span>
        <span className="findings-title">{title}</span>
        <span className="findings-when">{formatWhen(finding.created_at)}</span>
      </div>
      {(finding.source || targetLabel) && (
        <div className="findings-meta">
          {finding.source && <span className="findings-meta-item">{finding.source}</span>}
          {targetLabel && <span className="findings-meta-item">{targetLabel}</span>}
        </div>
      )}
      {description && <p className="findings-desc">{description}</p>}
      {evidence && (
        <details className="findings-evidence">
          <summary>{t('trace.findings.evidenceToggle')}</summary>
          <pre className="findings-evidence-body">{evidence}</pre>
        </details>
      )}
    </li>
  )
}
