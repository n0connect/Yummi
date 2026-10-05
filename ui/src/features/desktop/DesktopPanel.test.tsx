import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { t } from '../../shared/i18n/strings'
import { DesktopPanel } from './DesktopPanel'

const mockDisconnect = vi.fn()
const mockAddEventListener = vi.fn()
const mockRemoveEventListener = vi.fn()

vi.mock('@novnc/novnc', () => {
  return {
    default: vi.fn().mockImplementation(() => ({
      scaleViewport: false,
      resizeSession: false,
      disconnect: mockDisconnect,
      addEventListener: mockAddEventListener,
      removeEventListener: mockRemoveEventListener,
    })),
  }
})

describe('DesktopPanel', () => {
  it('renders locked overlay by default without connecting to RFB', () => {
    render(<DesktopPanel />)

    expect(screen.getByText(t('desktopPanel.lockedTitle'))).toBeTruthy()
    expect(screen.getByText(t('desktopPanel.clickToActivate'))).toBeTruthy()
    expect(screen.getByText(t('desktopPanel.standby'))).toBeTruthy()
  })

  it('activates and connects RFB when locked overlay is clicked', async () => {
    const RFB = (await import('@novnc/novnc')).default
    render(<DesktopPanel />)

    const overlay = screen.getByRole('button', { name: t('desktopPanel.clickToActivate') })
    fireEvent.click(overlay)

    expect(RFB).toHaveBeenCalled()
    expect(screen.queryByText(t('desktopPanel.lockedTitle'))).toBeNull()
    expect(screen.getByRole('button', { name: t('desktopPanel.hideDesktop') })).toBeTruthy()
  })

  it('hides desktop and disconnects RFB when hide button is clicked', () => {
    render(<DesktopPanel />)

    const overlay = screen.getByRole('button', { name: t('desktopPanel.clickToActivate') })
    fireEvent.click(overlay)

    const hideBtn = screen.getByRole('button', { name: t('desktopPanel.hideDesktop') })
    fireEvent.click(hideBtn)

    expect(mockDisconnect).toHaveBeenCalled()
    expect(screen.getByText(t('desktopPanel.lockedTitle'))).toBeTruthy()
    expect(screen.getByText(t('desktopPanel.standby'))).toBeTruthy()
  })
})
