import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
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

  it('applies the open class when open', () => {
    const { container } = render(
      <SessionRail open onToggle={() => {}} activeSessionID="default" refreshKey={0} onSelect={() => {}} />,
    )
    expect(container.querySelector('.session-rail')?.classList.contains('open')).toBe(true)
  })

  it('invokes onToggle when the toggle is clicked', () => {
    let toggled = 0
    render(
      <SessionRail open={false} onToggle={() => { toggled++ }} activeSessionID="default" refreshKey={0} onSelect={() => {}} />,
    )
    screen.getByLabelText('Sohbet geçmişi').click()
    expect(toggled).toBe(1)
  })
})
