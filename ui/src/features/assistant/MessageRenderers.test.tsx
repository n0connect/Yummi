import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ExecutionRecord } from '../../entities/types'
import { ExecutionMessage } from './MessageRenderers'

describe('ExecutionMessage', () => {
  const baseExecution: ExecutionRecord = {
    id: 'exec-1',
    session_id: 'session-123',
    command: 'nmap -sV -sC -p 1-1000 198.51.100.10',
    status: 'completed',
    output: 'Starting Nmap 7.94SVN\nNmap scan report for 198.51.100.10\nHost is up.'
  }

  it('renders completed command summary row visibly and collapsed by default', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    // The command and status should be visible in the summary
    expect(screen.getByText('nmap -sV -sC -p 1-1000 198.51.100.10')).toBeTruthy()
    const summaryBtn = screen.getByRole('button')
    expect(summaryBtn).toBeTruthy()

    // By default, completed execution output is collapsed
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('toggles detail visibility on click', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    const summaryBtn = screen.getByRole('button')
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()

    // Click to expand
    fireEvent.click(summaryBtn)
    expect(screen.getByText(/Starting Nmap 7.94SVN/)).toBeTruthy()

    // Click to collapse
    fireEvent.click(summaryBtn)
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('toggles detail visibility on Enter and Space keypress', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    const summaryBtn = screen.getByRole('button')

    // Press Enter to expand
    fireEvent.keyDown(summaryBtn, { key: 'Enter' })
    expect(screen.getByText(/Starting Nmap 7.94SVN/)).toBeTruthy()

    // Press Space to collapse
    fireEvent.keyDown(summaryBtn, { key: ' ' })
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('renders running execution open by default with live indicator', () => {
    const runningExecution: ExecutionRecord = {
      ...baseExecution,
      id: 'exec-running',
      status: 'running',
      elapsed_secs: 14
    }

    render(
      <ExecutionMessage
        content=""
        execution={runningExecution}
        liveOutput="Connecting to target..."
      />
    )

    // Live output should be rendered without needing a click
    expect(screen.getByText(/Connecting to target.../)).toBeTruthy()
  })
})
