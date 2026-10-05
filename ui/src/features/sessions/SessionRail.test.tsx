import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SessionRail } from './SessionRail'

// Mock-free, per the suite's no-mocks rule: a closed rail never calls load()
// (the fetch effect is gated on open), so these assertions exercise real
// render + prop wiring without any network stubbing.
describe('SessionRail', () => {
  it('renders the toggle and stays collapsed when closed', () => {
    const { container } = render(
      <SessionRail open={false} onToggle={() => {}} activeSessionID="default" refreshKey={0} onSelect={() => {}} />,
    )
    // The toggle is always present so the operator can open the rail.
    expect(screen.getByLabelText('Sohbet geçmişi')).toBeTruthy()
    // Closed → the aside lacks the .open class that slides it into view.
    const aside = container.querySelector('.session-rail')
    expect(aside).toBeTruthy()
    expect(aside?.classList.contains('open')).toBe(false)
  })

  it('applies the open class when open', async () => {
    const { container } = render(
      <SessionRail open onToggle={() => {}} activeSessionID="default" refreshKey={0} onSelect={() => {}} />,
    )
    expect(container.querySelector('.session-rail')?.classList.contains('open')).toBe(true)
    // Wait for the real, intentionally unmocked fetch to settle so no state
    // update escapes the test's act() boundary.
    await screen.findByText('Sohbetler yüklenemedi.')
  })

  it('invokes onToggle when the toggle is clicked', () => {
    let toggled = 0
    render(
      <SessionRail open={false} onToggle={() => { toggled++ }} activeSessionID="default" refreshKey={0} onSelect={() => {}} />,
    )
    fireEvent.click(screen.getByLabelText('Sohbet geçmişi'))
    expect(toggled).toBe(1)
  })

  it('renders pin, export, archive, delete action buttons for sessions', async () => {
    const mockSessions = [
      { id: 's1', name: 'Pentest Session Alpha', status: 'active', created_at: 1700000000, updated_at: 1700000000 },
    ]
    const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const urlStr = url.toString()
      if (urlStr.includes('/api/v1/sessions/s1') && init?.method === 'PATCH') {
        return new Response(JSON.stringify({ id: 's1', status: 'closed' }), { status: 200 })
      }
      if (urlStr.includes('/api/v1/sessions/s1') && init?.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      if (urlStr.includes('/api/v1/sessions') && (!init || !init.method || init.method === 'GET')) {
        return new Response(JSON.stringify(mockSessions), { status: 200 })
      }
      return new Response(JSON.stringify([]), { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    try {
      const { container } = render(
        <SessionRail open onToggle={() => {}} activeSessionID="s1" refreshKey={0} onSelect={() => {}} />,
      )

      await screen.findByText('Pentest Session Alpha')

      const pinBtn = screen.getByLabelText('Sohbeti başa sabitle')
      const exportBtn = screen.getByLabelText('Sohbeti dışa aktar')
      const archiveBtn = screen.getByLabelText('Sohbeti arşivle')
      const deleteBtn = screen.getByLabelText('Sohbeti sil')

      expect(pinBtn).toBeTruthy()
      expect(exportBtn).toBeTruthy()
      expect(archiveBtn).toBeTruthy()
      expect(deleteBtn).toBeTruthy()

      // Pin toggles state
      fireEvent.click(pinBtn)
      expect(container.querySelector('.rail-item.pinned')).toBeTruthy()

      // Delete requires confirmation click
      fireEvent.click(deleteBtn)
      expect(screen.getByLabelText('Silmek için tekrar tıklayın')).toBeTruthy()
      fireEvent.click(screen.getByLabelText('Silmek için tekrar tıklayın'))

      expect(fetchMock).toHaveBeenCalledWith('/api/v1/sessions/s1', expect.objectContaining({ method: 'DELETE' }))
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
