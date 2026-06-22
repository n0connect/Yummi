import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useGatewayRules } from './useGatewayData'

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
