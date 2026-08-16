import { useCallback, useEffect, useState } from 'react'

export type Theme = 'light' | 'dark'

const STORAGE_KEY = 'yummi.theme'

function isTheme(value: unknown): value is Theme {
  return value === 'light' || value === 'dark'
}

function readStoredTheme(): Theme | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    return isTheme(raw) ? raw : null
  } catch {
    // localStorage can throw in private-browsing / storage-restricted
    // contexts (e.g. Safari private mode) — degrade to system-preference-only,
    // same nil-safe-degradation pattern the rest of the app follows.
    return null
  }
}

function systemTheme(): Theme {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: light)').matches
    ? 'light'
    : 'dark'
}

function applyThemeAttribute(theme: Theme | null): void {
  const root = document.documentElement
  if (theme) root.setAttribute('data-theme', theme)
  else root.removeAttribute('data-theme')
}

/**
 * Tracks the app's effective color theme: an explicit operator override
 * (persisted in localStorage, wins once set) layered over the OS
 * `prefers-color-scheme` as the default. The CSS half of this lives in the
 * LIGHT THEME block of app.css — a `prefers-color-scheme: light` media query
 * for the auto case, plus a `[data-theme]` attribute on <html> (set here)
 * that overrides it either direction once the operator picks explicitly.
 *
 * Deliberately runtime-toggleable rather than build-time like strings.ts's
 * language dictionary: theme has no server-rendered surface to keep in sync,
 * and following the operator's OS preference by default is the whole point
 * — a build-time choice can't do that.
 */
export function useTheme(): { theme: Theme; toggleTheme: () => void } {
  const [override, setOverride] = useState<Theme | null>(() => readStoredTheme())
  const [system, setSystem] = useState<Theme>(() => systemTheme())

  // Keeps the "no override" (auto) case live if the OS preference changes
  // while the app is open.
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const mql = window.matchMedia('(prefers-color-scheme: light)')
    const onChange = (): void => setSystem(mql.matches ? 'light' : 'dark')
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [])

  useEffect(() => {
    applyThemeAttribute(override)
  }, [override])

  const theme = override ?? system

  const toggleTheme = useCallback(() => {
    const next: Theme = theme === 'light' ? 'dark' : 'light'
    setOverride(next)
    try {
      window.localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // Best-effort persistence only — the in-memory override still applies
      // for the rest of this session even if it can't be saved.
    }
  }, [theme])

  return { theme, toggleTheme }
}
