import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { t } from '../../shared/i18n/strings'
import { ProxyBridgePanel } from './ProxyBridgePanel'

describe('ProxyBridgePanel', () => {
  it('renders proxy bridge status when API responds', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementationOnce(() =>
      Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            status: 'active',
            mode: 'bridge',
            ca_installed: true,
            supported_protocols: ['http', 'https'],
            supported_sources: ['caido', 'burp', 'curl']
          })
      } as Response)
    )

    render(<ProxyBridgePanel />)

    await waitFor(() => {
      expect(screen.getByText(new RegExp(t('trace.proxy.ready')))).toBeTruthy()
    })
    expect(screen.getByText('(bridge)')).toBeTruthy()
  })

  it('renders empty dash when API fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementationOnce(() =>
      Promise.reject(new Error('Network error'))
    )

    render(<ProxyBridgePanel />)

    await waitFor(() => {
      expect(screen.getByText(t('trace.emptyDash'))).toBeTruthy()
    })
  })
})
