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
    expect(screen.getByText(t('desktopPanel.lockedHint'))).toBeTruthy()
    expect(screen.getByRole('button', { name: t('desktopPanel.clickToActivate') })).toBeTruthy()
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

  // Host->sandbox file sharing (VPN configs, keys, ...): available even
  // while locked/standby — it's a plain file copy, not a VNC session.
  describe('file upload to the sandbox Desktop', () => {
    it('renders the upload button before the desktop is even unlocked', () => {
      render(<DesktopPanel />)
      expect(screen.getByRole('button', { name: t('desktopPanel.uploadButton') })).toBeTruthy()
    })

    it('uploads a file picked via the hidden input and shows a success note', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ filename: 'vpn-config.ovpn' }), { status: 200 }),
      )
      vi.stubGlobal('fetch', fetchMock)
      try {
        render(<DesktopPanel />)
        const file = new File(['client\n'], 'vpn-config.ovpn', { type: 'text/plain' })
        const input = document.querySelector('input[type="file"]') as HTMLInputElement
        fireEvent.change(input, { target: { files: [file] } })

        await screen.findByText(`${t('desktopPanel.uploadSuccessPrefix')}vpn-config.ovpn`)
        expect(fetchMock).toHaveBeenCalledWith(
          expect.stringContaining('/api/v1/sandbox/upload'),
          expect.objectContaining({ method: 'POST' }),
        )
      } finally {
        vi.unstubAllGlobals()
      }
    })

    it('shows an error note when the upload fails', async () => {
      const fetchMock = vi.fn(async () => new Response('target not reachable', { status: 502 }))
      vi.stubGlobal('fetch', fetchMock)
      try {
        render(<DesktopPanel />)
        const file = new File(['x'], 'vpn-config.ovpn')
        const input = document.querySelector('input[type="file"]') as HTMLInputElement
        fireEvent.change(input, { target: { files: [file] } })

        await screen.findByText(new RegExp(`^${t('desktopPanel.uploadErrorPrefix')}`))
      } finally {
        vi.unstubAllGlobals()
      }
    })

    it('uploads a dragged-and-dropped file', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ filename: 'dropped.ovpn' }), { status: 200 }),
      )
      vi.stubGlobal('fetch', fetchMock)
      try {
        const { container } = render(<DesktopPanel />)
        const section = container.querySelector('.desktop-panel') as HTMLElement
        const file = new File(['x'], 'dropped.ovpn')

        fireEvent.drop(section, { dataTransfer: { files: [file], types: ['Files'] } })

        await screen.findByText(`${t('desktopPanel.uploadSuccessPrefix')}dropped.ovpn`)
      } finally {
        vi.unstubAllGlobals()
      }
    })
  })
})
