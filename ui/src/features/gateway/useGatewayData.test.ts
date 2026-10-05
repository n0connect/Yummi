import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useGatewayRules, useEgressMode, setEgressMode } from './useGatewayData'

describe('useGatewayRules', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ rules: [{ id: 'r1', action: 'allow', network: '10.0.0.5/32', description: '[manual] test', expires_at: 9999, source: 'manual' }], status: 'connected' }), { status: 200 })
    ))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('fetches and returns rules', async () => {
    const { result } = renderHook(() => useGatewayRules())
    await waitFor(() => expect(result.current.rules.length).toBe(1))
    expect(result.current.rules[0].source).toBe('manual')
  })
})

describe('useEgressMode', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (url: string | URL | Request) => {
      const urlStr = String(url)
      if (urlStr.includes('/api/v1/gateway/egress-mode')) {
        return new Response(JSON.stringify({ mode: 'block_except_rules', status: 'connected' }), { status: 200 })
      }
      return new Response('{}', { status: 200 })
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('fetches and returns the 4-mode egress state', async () => {
    const { result } = renderHook(() => useEgressMode())
    await waitFor(() => expect(result.current.mode).toBe('block_except_rules'))
    expect(result.current.status).toBe('connected')
  })

  it('sends POST request when setEgressMode is invoked', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await setEgressMode('block_all')
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/gateway/egress-mode'),
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ mode: 'block_all' })
      })
    )
  })
})
