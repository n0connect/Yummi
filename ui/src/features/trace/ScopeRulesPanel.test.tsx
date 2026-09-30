import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { t } from '../../shared/i18n/strings'
import { ScopeRulesPanel } from './ScopeRulesPanel'

describe('ScopeRulesPanel', () => {
  it('renders rules list and tests live classification', async () => {
    const rules = [
      {
        id: 'sr-1',
        session_id: 's1',
        pattern: '*.example.com',
        kind: 'include' as const,
        note: 'Main scope',
        created_at: 1_700_000_100,
      },
      {
        id: 'sr-2',
        session_id: 's1',
        pattern: 'admin.example.com',
        kind: 'exclude' as const,
        note: 'Out of scope',
        created_at: 1_700_000_200,
      },
    ]

    vi.spyOn(global, 'fetch').mockImplementation((url) => {
      const urlStr = String(url)
      if (urlStr.includes('/scope-rules/classify')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              'api.example.com': {
                in_scope: true,
                reason: 'matched wildcard include',
                matched_rule: '*.example.com',
              },
            }),
        } as Response)
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(rules),
      } as Response)
    })

    render(<ScopeRulesPanel sessionID="s1" />)

    // Precedence notice is rendered
    expect(screen.getByText(t('trace.scopeRules.precedenceNotice'))).toBeTruthy()

    await waitFor(() => {
      expect(screen.getByText('*.example.com')).toBeTruthy()
      expect(screen.getByText('admin.example.com')).toBeTruthy()
    })

    // Test live classification
    const testInput = screen.getByPlaceholderText(t('trace.scopeRules.testPlaceholder'))
    fireEvent.change(testInput, { target: { value: 'api.example.com' } })

    const classifyBtn = screen.getByRole('button', { name: t('trace.scopeRules.testButton') })
    fireEvent.click(classifyBtn)

    await waitFor(() => {
      expect(screen.getByText(new RegExp(t('trace.scopeRules.inScope')))).toBeTruthy()
    })
  })

  it('rejects bare wildcard pattern submission with validation alert', async () => {
    vi.spyOn(global, 'fetch').mockImplementation(() =>
      Promise.resolve({
        ok: true,
        json: () => Promise.resolve([]),
      } as Response)
    )

    render(<ScopeRulesPanel sessionID="s1" />)

    const patternInput = screen.getByPlaceholderText(t('trace.scopeRules.patternPlaceholder'))
    fireEvent.change(patternInput, { target: { value: '*' } })

    const addBtn = screen.getByRole('button', { name: t('trace.scopeRules.add') })
    fireEvent.click(addBtn)

    await waitFor(() => {
      expect(screen.getByText(t('trace.scopeRules.invalidPattern'))).toBeTruthy()
    })
  })

  it('handles 1000-character input in live classifier without issue', async () => {
    const longHostname = 'a'.repeat(980) + '.example.com'
    vi.spyOn(global, 'fetch').mockImplementation((url) => {
      const urlStr = String(url)
      if (urlStr.includes('/scope-rules/classify')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              [longHostname]: {
                in_scope: false,
                reason: 'whitelist miss',
              },
            }),
        } as Response)
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve([]),
      } as Response)
    })

    render(<ScopeRulesPanel sessionID="s1" />)

    const testInput = screen.getByPlaceholderText(t('trace.scopeRules.testPlaceholder'))
    fireEvent.change(testInput, { target: { value: longHostname } })

    const classifyBtn = screen.getByRole('button', { name: t('trace.scopeRules.testButton') })
    fireEvent.click(classifyBtn)

    await waitFor(() => {
      expect(screen.getByText(new RegExp(t('trace.scopeRules.outScope')))).toBeTruthy()
    })
  })
})
