import { act, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { App } from './App'

describe('App', () => {
  it('renders yummi header', async () => {
    render(<App />)
    expect(screen.getByText(/^YUMMI$/)).toBeTruthy()
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('complementary')).toBeTruthy()
  })
})
