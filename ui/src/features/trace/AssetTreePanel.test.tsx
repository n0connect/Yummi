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

  it('renders empty message when no assets exist', () => {
    render(<AssetTreePanel assets={[]} />)
    expect(screen.getByText(t('trace.assets.empty'))).toBeTruthy()
  })
})
