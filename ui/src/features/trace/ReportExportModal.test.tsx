import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { t } from '../../shared/i18n/strings'
import { ReportExportModal } from './ReportExportModal'

describe('ReportExportModal', () => {
  it('renders report modal and loads report markdown content', async () => {
    const fakeMarkdown = '# Vulnerability Assessment Report\n\nTarget: example.com'
    vi.spyOn(globalThis, 'fetch').mockImplementationOnce(() =>
      Promise.resolve({
        ok: true,
        text: () => Promise.resolve(fakeMarkdown)
      } as Response)
    )

    render(<ReportExportModal sessionID="s-123" onClose={() => {}} />)

    expect(screen.getByText(t('trace.reports.modalTitle'))).toBeTruthy()

    await waitFor(() => {
      expect(screen.getByText(/Vulnerability Assessment Report/)).toBeTruthy()
    })
  })
})
