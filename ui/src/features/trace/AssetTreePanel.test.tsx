import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AssetTree } from '../../entities/types'
import { t } from '../../shared/i18n/strings'
import { AssetTreePanel } from './AssetTreePanel'

const sampleTree: AssetTree[] = [
  {
    id: 'ast-1',
    session_id: 's1',
    type: 'domain',
    value: 'example.com',
    created_at: 1_700_000_100,
    children: [
      {
        id: 'ast-2',
        session_id: 's1',
        type: 'subdomain',
        value: 'api.example.com',
        parent_id: 'ast-1',
        created_at: 1_700_000_200,
        children: [
          {
            id: 'ast-3',
            session_id: 's1',
            type: 'endpoint',
            value: '/v1/users',
            parent_id: 'ast-2',
            created_at: 1_700_000_300,
            children: []
          }
        ]
      }
    ]
  }
]

describe('AssetTreePanel', () => {
  it('renders asset tree nodes and allows searching', () => {
    render(<AssetTreePanel assets={sampleTree} />)

    expect(screen.getByText('example.com')).toBeTruthy()
    expect(screen.getByText('api.example.com')).toBeTruthy()
    expect(screen.getByText('/v1/users')).toBeTruthy()

    // Test filter input
    const searchInput = screen.getByPlaceholderText(t('trace.assets.searchPlaceholder'))
    fireEvent.change(searchInput, { target: { value: 'api' } })

    expect(screen.getByText('api.example.com')).toBeTruthy()
    expect(screen.queryByText('/v1/users')).toBeNull()
  })

  it('renders empty message when no assets exist (0 asset case)', () => {
    render(<AssetTreePanel assets={[]} />)
    expect(screen.getByText(t('trace.assets.empty'))).toBeTruthy()
  })

  it('handles regex-special characters in search box without crashing', () => {
    render(<AssetTreePanel assets={sampleTree} />)
    const searchInput = screen.getByPlaceholderText(t('trace.assets.searchPlaceholder'))

    // Test characters that would break if passed to new RegExp(): (, *, \, [, ?, +, $, ^
    const specialChars = ['(', '*', '\\', '[', '?', '+', '$', '^', '{', '|']
    for (const char of specialChars) {
      expect(() => {
        fireEvent.change(searchInput, { target: { value: char } })
      }).not.toThrow()
    }

    // Exact match for a special character
    fireEvent.change(searchInput, { target: { value: 'nonexistent-pattern-(((' } })
    expect(screen.queryByText('example.com')).toBeNull()
  })

  it('handles deeply nested subdomain chain (10 levels deep) without stack overflow', () => {
    // Build a 10-level nested chain: level-10 -> ... -> level-1 -> root
    let deepest: AssetTree = {
      id: 'ast-deep-10',
      session_id: 's1',
      type: 'subdomain',
      value: 'l10.deep.target.com',
      created_at: 1_700_000_010,
      children: []
    }

    for (let i = 9; i >= 1; i--) {
      deepest = {
        id: `ast-deep-${i}`,
        session_id: 's1',
        type: 'subdomain',
        value: `l${i}.deep.target.com`,
        created_at: 1_700_000_000 + i,
        children: [deepest]
      }
    }

    const root: AssetTree = {
      id: 'ast-deep-root',
      session_id: 's1',
      type: 'domain',
      value: 'deep.target.com',
      created_at: 1_700_000_000,
      children: [deepest]
    }

    render(<AssetTreePanel assets={[root]} />)
    expect(screen.getByText('deep.target.com')).toBeTruthy()
    expect(screen.getByText('l10.deep.target.com')).toBeTruthy()
  })

  it('handles large volume of assets (1,000 nodes) with performance cap', () => {
    const largeTree: AssetTree[] = Array.from({ length: 1000 }, (_, i) => ({
      id: `ast-vol-${i}`,
      session_id: 's1',
      type: 'subdomain',
      value: `host-${i}.example.com`,
      created_at: 1_700_000_000 + i,
      children: []
    }))

    render(<AssetTreePanel assets={largeTree} />)
    // First node should be present
    expect(screen.getByText('host-0.example.com')).toBeTruthy()
    // Should display capped notice for 500 of 1000
    const notice = screen.getByText((content) => content.includes('500') && content.includes('1000'))
    expect(notice).toBeTruthy()
  })
})
