import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTheme } from './useTheme'

const STORAGE_KEY = 'yummi.theme'

/** Minimal but shape-complete matchMedia stub — jsdom has no real media
 * feature evaluation, so tests drive `prefers-color-scheme` explicitly. */
function stubMatchMedia(prefersLight: boolean): void {
  vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
    matches: query.includes('light') ? prefersLight : !prefersLight,
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })))
}

/** jsdom's real `localStorage` throws SecurityError on an opaque origin
 * (vitest's jsdom environment has no `url` configured here), so it's
 * unusable as-is in this test harness — same category of environment gap as
 * setup.ts's scrollIntoView/ResizeObserver stubs, not a mock of anything
 * under test. A tiny in-memory Storage stand-in sidesteps it. */
function createMemoryStorage(): Storage {
  const store = new Map<string, string>()
  return {
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    setItem: (key: string, value: string) => { store.set(key, String(value)) },
    removeItem: (key: string) => { store.delete(key) },
    clear: () => { store.clear() },
    key: (index: number) => Array.from(store.keys())[index] ?? null,
    get length() { return store.size },
  } as Storage
}

describe('useTheme', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', createMemoryStorage())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.documentElement.removeAttribute('data-theme')
  })

  it('follows the OS preference (light) when nothing is stored', () => {
    stubMatchMedia(true)
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('light')
    // Auto mode never sets an explicit attribute — the CSS media query
    // alone governs it.
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('follows the OS preference (dark) when nothing is stored', () => {
    stubMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('a stored override wins over the OS preference', () => {
    stubMatchMedia(true) // OS prefers light
    window.localStorage.setItem(STORAGE_KEY, 'dark')
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })

  it('ignores a corrupt stored value and falls back to the OS preference', () => {
    stubMatchMedia(false)
    window.localStorage.setItem(STORAGE_KEY, 'purple')
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('toggleTheme flips the theme, persists it, and sets the data-theme attribute', () => {
    stubMatchMedia(false) // system: dark
    const { result } = renderHook(() => useTheme())
    expect(result.current.theme).toBe('dark')

    act(() => result.current.toggleTheme())

    expect(result.current.theme).toBe('light')
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('toggling twice returns to an explicit override matching the original, still persisted', () => {
    stubMatchMedia(true) // system: light
    const { result } = renderHook(() => useTheme())

    act(() => result.current.toggleTheme()) // -> explicit dark
    expect(result.current.theme).toBe('dark')

    act(() => result.current.toggleTheme()) // -> explicit light
    expect(result.current.theme).toBe('light')
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })
})
