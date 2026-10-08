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

  it('toggles archive view and displays archived sessions', async () => {
    const mockSessions = [
      { id: 's1', name: 'Active Scan', status: 'active', created_at: 1700000000, updated_at: 1700000000 },
      { id: 's2', name: 'Archived Pentest', status: 'closed', created_at: 1700000000, updated_at: 1700000000 },
    ]
    const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const urlStr = url.toString()
      if (urlStr.includes('/api/v1/sessions/s2') && init?.method === 'PATCH') {
        return new Response(JSON.stringify({ id: 's2', status: 'active' }), { status: 200 })
      }
      if (urlStr.includes('/api/v1/sessions') && (!init || !init.method || init.method === 'GET')) {
        return new Response(JSON.stringify(mockSessions), { status: 200 })
      }
      return new Response(JSON.stringify([]), { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    try {
      render(
        <SessionRail open onToggle={() => {}} activeSessionID="s1" refreshKey={0} onSelect={() => {}} />,
      )

      await screen.findByText('Active Scan')
      expect(screen.queryByText('Archived Pentest')).toBeNull()

      const archiveToggleBtn = screen.getByTitle('Arşivlenmiş sohbetler')
      expect(archiveToggleBtn).toBeTruthy()
      expect(screen.getByText('1')).toBeTruthy() // archive count

      // Switch to archive view
      fireEvent.click(archiveToggleBtn)
      await screen.findByText('Archived Pentest')
      expect(screen.queryByText('Active Scan')).toBeNull()

      // Unarchive button is present on the archived session
      const unarchiveBtn = screen.getByLabelText('Arşivden çıkar')
      expect(unarchiveBtn).toBeTruthy()
      fireEvent.click(unarchiveBtn)

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/sessions/s2',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify({ status: 'active' }),
        }),
      )
    } finally {
      vi.unstubAllGlobals()
    }
  })

  // Regression test: export used to fetch ONLY /messages, which silently
  // dropped every agent task, executed command, and finding — none of those
  // are ever persisted as chat_messages rows (see handleExport's own doc
  // comment). This proves the export now merges all four sources and orders
  // them by their real timestamp, not just whatever happened to be in the
  // messages table.
  it('exports the full session — messages, tasks, executions, and findings merged by time', async () => {
    const mockSessions = [
      { id: 's1', name: 'Pentest Session Alpha', status: 'active', created_at: 1700000000, updated_at: 1700000000 },
    ]
    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      const urlStr = url.toString()
      if (urlStr.includes('/sessions/s1/messages')) {
        return new Response(
          JSON.stringify([
            { id: 1, session_id: 's1', role: 'user', content: 'scan the target', created_at: 1000 },
          ]),
          { status: 200 },
        )
      }
      if (urlStr.includes('/sessions/s1/tasks')) {
        return new Response(
          JSON.stringify([
            {
              id: 't1', session_id: 's1', goal: 'compromise the target', status: 'completed',
              steps_used: 5, step_budget: 15, summary: 'got root', created_at: 2000,
            },
          ]),
          { status: 200 },
        )
      }
      if (urlStr.includes('/sessions/s1/executions')) {
        return new Response(
          JSON.stringify([
            {
              id: 'e1', session_id: 's1', status: 'completed', command: 'nmap -sV 198.51.100.10',
              sanitized_output: 'port 80 open', created_at: 1500,
            },
          ]),
          { status: 200 },
        )
      }
      if (urlStr.includes('/sessions/s1/findings')) {
        return new Response(
          JSON.stringify([
            { id: 'f1', session_id: 's1', severity: 'high', title: 'Apache path traversal', created_at: 2500 },
          ]),
          { status: 200 },
        )
      }
      if (urlStr.includes('/api/v1/sessions') && !urlStr.includes('/s1/')) {
        return new Response(JSON.stringify(mockSessions), { status: 200 })
      }
      return new Response(JSON.stringify([]), { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    let capturedBlob: Blob | null = null
    const originalCreateObjectURL = URL.createObjectURL
    URL.createObjectURL = vi.fn((blob: Blob) => {
      capturedBlob = blob
      return 'blob:mock'
    })
    const originalRevoke = URL.revokeObjectURL
    URL.revokeObjectURL = vi.fn()

    try {
      render(
        <SessionRail open onToggle={() => {}} activeSessionID="s1" refreshKey={0} onSelect={() => {}} />,
      )
      await screen.findByText('Pentest Session Alpha')

      fireEvent.click(screen.getByLabelText('Sohbeti dışa aktar'))

      // The export's fetches are async (Promise.all inside the handler) —
      // wait for the blob to actually land before asserting on it.
      await vi.waitFor(() => expect(capturedBlob).not.toBeNull())

      // jsdom's Blob polyfill has no .text(), and wrapping it in a Response
      // here stringifies it instead of reading its content — FileReader is
      // the one Blob-reading API jsdom actually implements correctly.
      const text = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result))
        reader.onerror = () => reject(reader.error)
        reader.readAsText(capturedBlob!)
      })
      expect(text).toContain('scan the target')
      expect(text).toContain('compromise the target')
      expect(text).toContain('got root')
      expect(text).toContain('nmap -sV 198.51.100.10')
      expect(text).toContain('port 80 open')
      expect(text).toContain('Apache path traversal')

      // Chronological order: message(1000) -> execution(1500) -> task(2000) -> finding(2500)
      const order = ['scan the target', 'nmap -sV 198.51.100.10', 'compromise the target', 'Apache path traversal']
      const positions = order.map((needle) => text.indexOf(needle))
      expect(positions).toEqual([...positions].sort((a, b) => a - b))
    } finally {
      URL.createObjectURL = originalCreateObjectURL
      URL.revokeObjectURL = originalRevoke
      vi.unstubAllGlobals()
    }
  })
})
