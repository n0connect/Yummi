import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { usePendingSkills } from './usePendingSkills'

const samplePending = {
  id: 'p1', status: 'pending', origin: 'auto', task_id: 't1', session_id: 's1',
  name: 'recon-sweep', draft_yaml: 'name: recon-sweep\n', trajectory: '{}',
  created_at: 1700000000,
}

describe('usePendingSkills', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches and returns pending drafts', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify([samplePending]), { status: 200 })
    ))
    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending.length).toBe(1))
    expect(result.current.pending[0].name).toBe('recon-sweep')
  })

  it('degrades to an empty list on fetch failure', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('network down') }))
    const { result } = renderHook(() => usePendingSkills())
    await waitFor(() => expect(result.current.pending).toEqual([]))
  })

  it('approve() posts to the approve endpoint and refreshes', async () => {
    let listCalls = 0
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/v1/skills/pending' && !init) {
        listCalls++
        return new Response(JSON.stringify(listCalls === 1 ? [samplePending] : []), { status: 200 })
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
