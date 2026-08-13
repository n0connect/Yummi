import { useState, useEffect, useRef, useCallback } from 'react'
import type { LogLevel } from '../../entities/types'
import { useLogStream, filterEntries } from './useLogStream'
import { LogLine } from '../../shared/components/LogLine'
import { t } from '../../shared/i18n/strings'

type Filter = LogLevel | 'ALL'
const FILTERS: Filter[] = ['ALL', 'DEBUG', 'INFO', 'WARN', 'ERROR']

export function DebugLogPanel(): JSX.Element {
  const entries = useLogStream()
  const [activeFilter, setActiveFilter] = useState<Filter>('ALL')
  const [searchTerm, setSearchTerm] = useState('')
  const listRef = useRef<HTMLDivElement>(null)
  const autoScroll = useRef(true)

  const visible = filterEntries(entries, activeFilter).filter(e =>
    searchTerm === '' || e.msg.toLowerCase().includes(searchTerm.toLowerCase())
  )

  // Auto-scroll to bottom on new entries
  useEffect(() => {
    if (autoScroll.current && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight
    }
  }, [visible.length])

  const onScroll = useCallback(() => {
    if (!listRef.current) return
    const { scrollTop, scrollHeight, clientHeight } = listRef.current
    autoScroll.current = scrollHeight - scrollTop - clientHeight < 40
  }, [])

  return (
    <>
      <div className="panel-header">
        <span className="panel-header-title">{t('debugLog.title')}</span>
        <span className="panel-header-actions log-count">
          {visible.length}/{entries.length} {t('debugLog.linesSuffix')}
        </span>
      </div>
      <div className="log-filter-bar">
        {FILTERS.map(f => (
          <button
            key={f}
            className={`log-filter-btn${activeFilter === f ? ' active' : ''}`}
            onClick={() => setActiveFilter(f)}
          >
            {f}
          </button>
        ))}
        <input
          type="text"
          placeholder={t('debugLog.filterPlaceholder')}
          value={searchTerm}
          onChange={(e) => setSearchTerm(e.target.value)}
          className="log-search-input"
        />
      </div>
      <div className="log-list" ref={listRef} onScroll={onScroll}>
        {visible.length === 0 ? (
          <div className="log-empty">
            {entries.length === 0 ? t('debugLog.noEntries') : t('debugLog.noMatches')}
          </div>
        ) : (
          visible.map(e => <LogLine key={e._id} entry={e} />)
        )}
      </div>
    </>
  )
}
