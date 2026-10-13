import { useState } from 'react'
import { t } from '../../shared/i18n/strings'

export type Source = {
  source_url: string
  title: string
  excerpt: string
  retrieved_at: string
  sha256: string
  kind?: string
}

export function parseResearchSources(output: string): Source[] {
  try {
    const report: unknown = JSON.parse(output)
    if (!report || typeof report !== 'object') return []
    const value = report as { kind?: string; sources?: unknown }
    if (!['web_search', 'web_fetch'].includes(value.kind ?? '') || !Array.isArray(value.sources)) return []
    const kind = value.kind
    return value.sources.slice(0, 10).filter((source): source is Source => {
      if (!source || typeof source !== 'object') return false
      if (!['source_url', 'title', 'excerpt', 'retrieved_at', 'sha256'].every(key => typeof (source as Record<string, unknown>)[key] === 'string')) return false
      try {
        const url = new URL((source as Source).source_url)
        return url.protocol === 'https:' && !url.username && !url.password
      } catch { return false }
    }).map(s => (kind ? { ...s, kind } : s))
  } catch { return [] }
}

export function ResearchSources({ output }: { output: string }): JSX.Element | null {
  const sources = parseResearchSources(output)
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null)
  if (!sources.length) return null

  const handleCopy = (url: string) => {
    try {
      if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(url).catch(() => {})
      }
    } catch {}
    setCopiedUrl(url)
    setTimeout(() => setCopiedUrl(null), 2000)
  }

  return (
    <section className="research-sources" aria-label={t('research.sources')}>
      <div className="research-sources-header">
        <span className="research-sources-badge">
          {sources[0]?.kind === 'web_fetch' ? t('research.kindFetch') : t('research.kindSearch')}
        </span>
        <p className="research-sources-notice">{t('research.untrusted')}</p>
      </div>
      <div className="research-sources-list">
        {sources.map(source => {
          let host = ''
          try { host = new URL(source.source_url).hostname } catch {}
          const isCopied = copiedUrl === source.source_url
          return (
            <article key={source.source_url} className="research-source">
              <div className="research-source-head">
                <a href={source.source_url} target="_blank" rel="noopener noreferrer" className="research-source-title">
                  {source.title || host}
                </a>
                <button
                  type="button"
                  className="research-source-copy"
                  title={isCopied ? t('research.copied') : t('research.copyUrl')}
                  aria-label={isCopied ? t('research.copied') : t('research.copyUrl')}
                  onClick={() => handleCopy(source.source_url)}
                >
                  {isCopied ? '✓' : '📋'}
                </button>
              </div>
              <small className="research-source-meta">
                {host} · {source.retrieved_at} · SHA-256 {source.sha256.slice(0, 12)}
              </small>
              <p className="research-source-excerpt">{source.excerpt}</p>
            </article>
          )
        })}
      </div>
    </section>
  )
}
