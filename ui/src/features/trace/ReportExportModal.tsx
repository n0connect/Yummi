import { useState, useEffect, useCallback } from 'react'
import type { ReportFormat } from '../../entities/types'
import { t } from '../../shared/i18n/strings'
import './ReportExportModal.css'

interface ReportExportModalProps {
  sessionID: string
  onClose: () => void
}

const FORMAT_OPTIONS: { format: ReportFormat; labelKey: string }[] = [
  { format: 'hackerone', labelKey: 'trace.reports.hackerone' },
  { format: 'bugcrowd', labelKey: 'trace.reports.bugcrowd' },
  { format: 'executive', labelKey: 'trace.reports.executive' },
  { format: 'json', labelKey: 'trace.reports.json' },
]

export function ReportExportModal({ sessionID, onClose }: ReportExportModalProps): JSX.Element {
  const [format, setFormat] = useState<ReportFormat>('hackerone')
  const [content, setContent] = useState<string>('')
  const [loading, setLoading] = useState<boolean>(true)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState<boolean>(false)

  const fetchReport = useCallback(async (fmt: ReportFormat) => {
    setLoading(true)
    setError(null)
    try {
      const sid = sessionID || 'default'
      const res = await fetch(`/api/v1/sessions/${encodeURIComponent(sid)}/report?format=${fmt}`)
      if (!res.ok) {
        throw new Error(`Report generation failed (${res.status})`)
      }
      const text = await res.text()
      setContent(text)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to generate report')
    } finally {
      setLoading(false)
    }
  }, [sessionID])

  useEffect(() => {
    fetchReport(format)
  }, [format, fetchReport])

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Fallback
    }
  }

  const handleDownload = () => {
    const rawSid = sessionID || 'default'
    const safeSid = rawSid.replace(/[^a-zA-Z0-9_-]/g, '_') || 'default'
    const ext = format === 'json' ? 'json' : 'md'
    const blob = new Blob([content], { type: format === 'json' ? 'application/json' : 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `pentest-report-${safeSid}-${format}.${ext}`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  return (
    <div className="report-modal-backdrop" onClick={onClose} role="dialog" aria-modal="true">
      <div className="report-modal" onClick={e => e.stopPropagation()}>
        <div className="report-modal-head">
          <span className="report-modal-title">{t('trace.reports.modalTitle')}</span>
          <button type="button" className="small-button" onClick={onClose} aria-label="Close modal">
            X
          </button>
        </div>

        <div className="report-modal-body">
          <div className="report-format-select">
            <span className="report-format-label">{t('trace.reports.format')}:</span>
            <div className="report-btn-group">
              {FORMAT_OPTIONS.map(opt => (
                <button
                  key={opt.format}
                  type="button"
                  className={`report-format-btn ${format === opt.format ? 'active' : ''}`}
                  onClick={() => setFormat(opt.format)}
                >
                  {t(opt.labelKey as any)}
                </button>
              ))}
            </div>
          </div>

          {loading ? (
            <div className="trace-empty">{t('trace.reports.generating')}</div>
          ) : error ? (
            <div className="execution-threat" role="alert">{error}</div>
          ) : (
            <pre className="report-preview-box">{content}</pre>
          )}
        </div>

        <div className="report-modal-foot">
          <button
            type="button"
            className="report-btn-action report-btn-copy"
            onClick={handleCopy}
            disabled={loading || Boolean(error)}
          >
            {copied ? t('trace.reports.copied') : t('trace.reports.copy')}
          </button>
          <button
            type="button"
            className="report-btn-action report-btn-download"
            onClick={handleDownload}
            disabled={loading || Boolean(error)}
          >
            {t('trace.reports.download')}
          </button>
        </div>
      </div>
    </div>
  )
}
