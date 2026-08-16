import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Finding, Target } from '../../entities/types'
import { FindingsPanel } from './FindingsPanel'

// Mock-free: FindingsPanel is pure presentational (props in, no fetch), so
// these exercise the real component with real Finding/Target fixtures.
const findings: Finding[] = [
  {
    id: 'f-critical',
    session_id: 's1',
    target_id: 't-1',
    severity: 'critical',
    title: 'SQL injection in login form',
    description: 'Unauthenticated SQLi confirmed on /login.',
    evidence: 'sqlmap output: parameter username is vulnerable...',
    source: 'agent',
    created_at: 1_700_000_300
  },
  {
    id: 'f-low',
    session_id: 's1',
    target_id: 't-2',
    severity: 'low',
    title: 'Verbose server banner',
    source: 'manual',
    created_at: 1_700_000_100
  },
  {
    id: 'f-info',
    session_id: 's1',
    severity: 'info',
    title: 'Open port 80 observed',
    source: 'agent',
    created_at: 1_700_000_200
  }
]

const targets: Target[] = [
  { id: 't-1', session_id: 's1', value: '10.0.0.5', in_scope: true },
  { id: 't-2', session_id: 's1', value: 'example.internal', in_scope: true }
]

describe('FindingsPanel', () => {
  it('renders every finding with its severity badge and a per-severity count', () => {
    render(<FindingsPanel findings={findings} targets={targets} />)
    expect(screen.getByText('SQL injection in login form')).toBeTruthy()
    expect(screen.getByText('Verbose server banner')).toBeTruthy()
    expect(screen.getByText('Open port 80 observed')).toBeTruthy()

    // Severity chip for 'critical' shows a count of 1 among the three findings.
    const criticalChip = screen.getByRole('button', { name: /critical/i })
    expect(criticalChip.textContent).toContain('1')
  })

  it('filters out a severity when its chip is toggled off', () => {
    render(<FindingsPanel findings={findings} targets={targets} />)
    const criticalChip = screen.getByRole('button', { name: /critical/i })

    fireEvent.click(criticalChip)

    expect(screen.queryByText('SQL injection in login form')).toBeNull()
    expect(screen.getByText('Verbose server banner')).toBeTruthy()
    expect(screen.getByText('Open port 80 observed')).toBeTruthy()
  })

  it('shows the "no match" message when every severity is filtered out', () => {
    render(<FindingsPanel findings={findings} targets={targets} />)
    // Toggle every chip off.
    ;['critical', 'high', 'medium', 'low', 'info'].forEach((sev) => {
      fireEvent.click(screen.getByRole('button', { name: new RegExp(sev, 'i') }))
    })
    expect(screen.getByText('Filtrelere uyan bulgu yok.')).toBeTruthy()
  })

  it('resolves target_id to the target\'s human-readable value and filters by it', () => {
    const { container } = render(<FindingsPanel findings={findings} targets={targets} />)
    // "10.0.0.5" appears both as a select <option> and as the row's meta
    // label — assert against the rendered finding row specifically.
    const metaLabels = Array.from(container.querySelectorAll('.findings-meta-item')).map((el) => el.textContent)
    expect(metaLabels).toContain('10.0.0.5')
    expect(metaLabels).toContain('example.internal')

    const targetSelect = screen.getByLabelText('Hedef') as HTMLSelectElement
    fireEvent.change(targetSelect, { target: { value: 't-1' } })

    expect(screen.getByText('SQL injection in login form')).toBeTruthy()
    expect(screen.queryByText('Verbose server banner')).toBeNull()
  })

  it('keeps Evidence collapsed by default and expands it on click', () => {
    const { container } = render(<FindingsPanel findings={findings} targets={targets} />)
    const details = container.querySelector('details.findings-evidence') as HTMLDetailsElement
    expect(details).toBeTruthy()
    expect(details.open).toBe(false)

    fireEvent.click(screen.getByText('Kanıt'))
    expect(details.open).toBe(true)
  })

  it('sorts by severity (highest first) when that sort mode is chosen', () => {
    render(<FindingsPanel findings={findings} targets={targets} />)
    const sortSelect = screen.getByLabelText('Sırala') as HTMLSelectElement
    fireEvent.change(sortSelect, { target: { value: 'severity' } })

    const titles = screen.getAllByText(/injection|banner|Open port/).map((el) => el.textContent)
    expect(titles[0]).toBe('SQL injection in login form') // critical first
  })

  it('shows the empty-session dash when there are no findings at all', () => {
    render(<FindingsPanel findings={[]} targets={[]} />)
    expect(screen.getByText('— henüz yok —')).toBeTruthy()
  })
})
