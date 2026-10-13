import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { parseResearchSources, ResearchSources } from './ResearchSources'
import { t } from '../../shared/i18n/strings'

describe('research source evidence', () => {
  const source = { source_url: 'https://www.openssh.com/releasenotes.html', title: 'OpenSSH release notes', excerpt: '<script>untrusted text</script>', retrieved_at: '2026-10-13T12:00:00Z', sha256: 'a'.repeat(64) }
  it('renders cited evidence as escaped text and opens links safely', () => {
    const markup = renderToStaticMarkup(<ResearchSources output={JSON.stringify({ kind: 'web_search', sources: [source] })} />)
    expect(markup).toContain('https://www.openssh.com/releasenotes.html')
    expect(markup).toContain('noopener noreferrer')
    expect(markup).toContain('&lt;script&gt;')
    expect(markup).not.toContain('<script>')
    expect(markup).toContain('research-source-copy')
    expect(markup).toContain(t('research.kindSearch'))
  })
  it('renders fetch badge for web_fetch kind', () => {
    const markup = renderToStaticMarkup(<ResearchSources output={JSON.stringify({ kind: 'web_fetch', sources: [source] })} />)
    expect(markup).toContain(t('research.kindFetch'))
  })
  it('rejects malformed envelopes and executable source URLs', () => {
    expect(parseResearchSources('not JSON')).toEqual([])
    expect(parseResearchSources(JSON.stringify({ kind: 'web_search', sources: [{ ...source, source_url: 'javascript:alert(1)' }] }))).toEqual([])
    expect(parseResearchSources(JSON.stringify({ kind: 'web_search', sources: [{ ...source, source_url: 'https://user:secret@example.com' }] }))).toEqual([])
  })
})
