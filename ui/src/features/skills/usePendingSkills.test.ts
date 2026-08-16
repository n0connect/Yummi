import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { usePendingSkills } from './usePendingSkills'

const samplePending = {
  id: 'p1', status: 'pending', origin: 'auto', task_id: 't1', session_id: 's1',
  name: 'recon-sweep', draft_yaml: 'name: recon-sweep\n', trajectory: '{}',
  created_at: 1700000000,
}

const sampleRejected = {
  id: 'p2', status: 'rejected', origin: 'auto', task_id: 't2', session_id: 's1',
  name: 'evil-skill', draft_yaml: 'name: evil-skill\n', trajectory: '{}',
  threat_scan_result: 'flagged: credential exfiltration pattern',
  reject_reason: 'threat scan: flagged: credential exfiltration pattern',
  created_at: 1700000001, resolved_at: 1700000002,
}

// jsonResponse builds a fetch Response keyed by exact URL match, defaulting
// to an empty list for any GET this test doesn't care about (e.g. the
// hook's own useChatStream doesn't hit fetch, but a future addition here
// shouldn't need every test to enumerate every endpoint).
function routedFetch(routes: Record<string, unknown>) {
  return vi.fn(async (url: string) => {
    if (url in routes) return new Response(JSON.stringify(routes[url]), { status: 200 })
    return new Response(JSON.stringify([]), { status: 200 })
  })
}

describe('usePendingSkills', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches and returns pending drafts', async () => {
    vi.stubGlobal('fetch', routedFetch({ '/api/v1/skills/pending': [samplePending] }))
    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending.length).toBe(1))
    expect(result.current.pending[0].name).toBe('recon-sweep')
  })

  it('fetches and returns rejected drafts (audit trail) via a separate status=rejected call', async () => {
    vi.stubGlobal('fetch', routedFetch({
      '/api/v1/skills/pending': [samplePending],
      '/api/v1/skills/pending?status=rejected': [sampleRejected],
    }))
    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.rejected.length).toBe(1))
    expect(result.current.rejected[0].name).toBe('evil-skill')
    expect(result.current.rejected[0].threat_scan_result).toContain('credential exfiltration')
  })

  it('degrades to empty lists on fetch failure', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('network down') }))
    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending).toEqual([]))
    expect(result.current.rejected).toEqual([])
  })

  it('approve() posts to the approve endpoint and refreshes', async () => {
    let listCalls = 0
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/v1/skills/pending' && !init) {
        listCalls++
        return new Response(JSON.stringify(listCalls === 1 ? [samplePending] : []), { status: 200 })
      }
      if (url === '/api/v1/skills/pending?status=rejected') {
        return new Response(JSON.stringify([]), { status: 200 })
      }
      if (url === '/api/v1/skills/pending/p1/approve') {
        return new Response(JSON.stringify({ resolved: true }), { status: 200 })
      }
      throw new Error(`unexpected fetch: ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending.length).toBe(1))

    await act(async () => { await result.current.approve('p1') })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/skills/pending/p1/approve', expect.objectContaining({ method: 'POST' }))
    await waitFor(() => expect(result.current.pending.length).toBe(0))
  })

  it('reject() posts to the reject endpoint with a reason', async () => {
    const fetchMock = vi.fn(async (url: string, _init?: RequestInit) => {
      if (url === '/api/v1/skills/pending') return new Response(JSON.stringify([samplePending]), { status: 200 })
      if (url === '/api/v1/skills/pending?status=rejected') return new Response(JSON.stringify([]), { status: 200 })
      if (url === '/api/v1/skills/pending/p1/reject') return new Response(JSON.stringify({ resolved: true }), { status: 200 })
      throw new Error(`unexpected fetch: ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending.length).toBe(1))

    await act(async () => { await result.current.reject('p1', 'too risky') })

    const call = fetchMock.mock.calls.find(c => c[0] === '/api/v1/skills/pending/p1/reject')
    expect(call).toBeTruthy()
    expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({ reason: 'too risky' })
  })
})
