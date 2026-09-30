import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { YummiLogo } from './YummiLogo'

describe('YummiLogo', () => {
  it('renders with default alt text and height', () => {
    render(<YummiLogo />)
    const img = screen.getByAltText('YUMMI')
    expect(img).toBeTruthy()
    expect(img.getAttribute('src')).toBeTruthy()
    expect(img.classList.contains('yummi-logo')).toBe(true)
    expect((img as HTMLElement).style.height).toBe('20px')
    expect((img as HTMLElement).style.width).toBe('auto')
  })

  it('accepts custom dimensions and classes', () => {
    render(<YummiLogo height={32} width={100} className="custom-mark" alt="Brand Logo" />)
    const img = screen.getByAltText('Brand Logo')
    expect(img).toBeTruthy()
    expect(img.classList.contains('custom-mark')).toBe(true)
    expect((img as HTMLElement).style.height).toBe('32px')
    expect((img as HTMLElement).style.width).toBe('100px')
  })
})
