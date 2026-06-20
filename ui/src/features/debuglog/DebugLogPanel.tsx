import { useState, useEffect, useRef, useCallback } from 'react'
import type { LogLevel } from '../../entities/types'
import { useLogStream, filterEntries } from './useLogStream'
import { LogLine } from '../../shared/components/LogLine'

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

  const clearLogs = () => {
    // Note: This only clears the view, not the actual log stream
    // Real implementation would need a clear function in useLogStream
  }

  return (
    <>
      <div className="panel-header">
        <span className="panel-header-title">Debug Log</span>
        <span className="panel-header-actions" style={{ color: 'var(--text-muted)', fontSize: '10px' }}>
          {visible.length}/{entries.length} lines
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
          placeholder="Filter..."
          value={searchTerm}
          onChange={(e) => setSearchTerm(e.target.value)}
          style={{
            marginLeft: 'auto',
            width: '120px',
            padding: '2px 8px',
            fontSize: '10px'
          }}
        />
      </div>
      <div className="log-list" ref={listRef} onScroll={onScroll}>
        {visible.length === 0 ? (
          <div className="log-empty">
            {entries.length === 0 ? 'No log entries yet...' : 'No matching entries'}
          </div>
        ) : (
          visible.map((e, i) => <LogLine key={i} entry={e} />)
        )}
      </div>
    </>
  )
}
