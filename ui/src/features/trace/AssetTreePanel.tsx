import { useState, useMemo } from 'react'
import type { AssetTree } from '../../entities/types'
import { t } from '../../shared/i18n/strings'
import './AssetTreePanel.css'

function typeBadgeClass(type?: string): string {
  switch (type) {
    case 'domain':       return 'asm-badge asm-type-domain'
    case 'subdomain':    return 'asm-badge asm-type-subdomain'
    case 'ip':           return 'asm-badge asm-type-ip'
    case 'port':         return 'asm-badge asm-type-port'
    case 'endpoint':     return 'asm-badge asm-type-endpoint'
    case 'parameter':    return 'asm-badge asm-type-parameter'
    case 'cloud_bucket': return 'asm-badge asm-type-cloud_bucket'
    case 'secret':       return 'asm-badge asm-type-secret'
    default:             return 'asm-badge asm-type-default'
  }
}

function typeLabel(type?: string): string {
  if (!type) return 'ASSET'
  switch (type) {
    case 'domain':       return 'DOM'
    case 'subdomain':    return 'SUB'
    case 'ip':           return 'IP'
    case 'port':         return 'PORT'
    case 'endpoint':     return 'EP'
    case 'parameter':    return 'PARAM'
    case 'cloud_bucket': return 'BUCKET'
    case 'secret':       return 'SECRET'
    default:             return type.slice(0, 5).toUpperCase()
  }
}

function nodeMatches(node: AssetTree, filter: string): boolean {
  if (!filter) return true
  const lower = filter.toLowerCase()
  if (node.value.toLowerCase().includes(lower)) return true
  if (node.type?.toLowerCase().includes(lower)) return true
  return Boolean(node.children?.some(c => nodeMatches(c, filter)))
}

function TreeNodeItem({
  node,
  filter,
  isRoot = false,
}: {
  node: AssetTree
  filter: string
  isRoot?: boolean
}): JSX.Element | null {
  const [expanded, setExpanded] = useState(true)

  const isVisible = useMemo(() => nodeMatches(node, filter), [node, filter])
  const matchingChildren = useMemo(() => {
    if (!node.children || node.children.length === 0) return []
    return node.children.filter(child => nodeMatches(child, filter))
  }, [node.children, filter])

  const hasChildren = matchingChildren.length > 0

  if (!isVisible) {
    return null
  }

  return (
    <div className={`asm-node ${isRoot ? 'asm-node-root' : ''}`}>
      <div className="asm-node-head">
        {hasChildren ? (
          <button
            type="button"
            className="asm-toggle"
            onClick={() => setExpanded(prev => !prev)}
            aria-label={expanded ? 'Collapse node' : 'Expand node'}
          >
            {expanded ? '[-]' : '[+]'}
          </button>
        ) : (
          <span className="asm-toggle" style={{ opacity: 0.3 }}>-</span>
        )}
        <span className={typeBadgeClass(node.type)}>{typeLabel(node.type)}</span>
        <span className="asm-val" title={node.value}>{node.value}</span>
      </div>
      {expanded && hasChildren && (
        <div className="asm-children">
          {matchingChildren.map(child => (
            <TreeNodeItem key={child.id} node={child} filter={filter} />
          ))}
        </div>
      )}
    </div>
  )
}

const MAX_ROOT_NODES = 500

export function AssetTreePanel({ assets }: { assets: AssetTree[] }): JSX.Element {
  const [filter, setFilter] = useState('')

  if (!assets || assets.length === 0) {
    return <div className="trace-empty">{t('trace.assets.empty')}</div>
  }

  const isCapped = !filter && assets.length > MAX_ROOT_NODES
  const displayedRoots = isCapped ? assets.slice(0, MAX_ROOT_NODES) : assets

  return (
    <div className="asm-panel">
      <input
        type="search"
        className="asm-search"
        placeholder={t('trace.assets.searchPlaceholder')}
        value={filter}
        onChange={e => setFilter(e.target.value)}
        aria-label="Filter assets"
      />
      {isCapped && (
        <div className="asm-capped-notice" style={{ fontSize: '10px', color: 'var(--text-muted, #888)', fontStyle: 'italic', padding: '2px 4px' }}>
          {t('trace.assets.cappedNotice').replace('%d', String(MAX_ROOT_NODES)).replace('%d', String(assets.length))}
        </div>
      )}
      <div className="asm-tree">
        {displayedRoots.map(rootNode => (
          <TreeNodeItem key={rootNode.id} node={rootNode} filter={filter} isRoot />
        ))}
      </div>
    </div>
  )
}
