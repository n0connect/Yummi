import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useOperatorBriefing } from './useOperatorBriefing'

// Only fetch (the environment boundary) is stubbed — the hook's own logic
// (query-string assembly, state transitions, error handling) is exercised
// for real, matching the project's no-mocks-of-our-own-code rule.
describe('useOperatorBriefing', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('assembles goal/tool/session into the query and stores the result', async () => {
    const seen: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        seen.push(url)
        return new Response(
          JSON.stringify({
            goal: 'recon',
            tool: 'nmap',
            skills: [{ name: 'recon' }],
            tactics: [{ id: 't1', tool: 'nmap', command_pattern: 'nmap <TARGET>', outcome: 'worked', hit_count: 3, last_used_at: 0, created_at: 0 }],
            findings: []
          }),
          { status: 200 }
        )
      })
    )

    const { result } = renderHook(() => useOperatorBriefing())
    await act(async () => {
      await result.current.query({ tool: 'nmap', goal: 'recon', session: 's1' })
    })

    expect(result.current.data?.skills[0].name).toBe('recon')
    expect(result.current.data?.tactics[0].tool).toBe('nmap')
    expect(result.current.queried).toBe(true)
    const url = seen[0]
    expect(url).toContain('tool=nmap')
    expect(url).toContain('goal=recon')
    expect(url).toContain('session=s1')
  })

  it('omits the legacy "default" session so global findings are not requested for it', async () => {
    const seen: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        seen.push(url)
        return new Response(JSON.stringify({ goal: '', tool: 'nmap', skills: [], tactics: [], findings: [] }), { status: 200 })
      })
    )
    const { result } = renderHook(() => useOperatorBriefing())
    await act(async () => {
      await result.current.query({ tool: 'nmap', session: 'default' })
    })
    expect(seen[0]).not.toContain('session=')
  })

  it('surfaces a failed request as an error and clears data', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('boom', { status: 503 })))
    const { result } = renderHook(() => useOperatorBriefing())
    await act(async () => {
      await result.current.query({ tool: 'nmap' })
    })
    await waitFor(() => expect(result.current.error).not.toBeNull())
    expect(result.current.data).toBeNull()
    expect(result.current.queried).toBe(true)
  })
})
