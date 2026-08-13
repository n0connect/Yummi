import { describe, expect, it } from 'vitest'
import { LANG, dictionaries, t } from './strings'

// The TypeScript `satisfies Record<keyof typeof en, string>` check on `tr`
// already makes a missing/extra key a compile error. This test is a runtime
// backstop for the same invariant — mirrors the project's other executable
// fitness-function tests (e.g. orchestrator's invariants_test.go) rather than
// trusting the type system alone.
describe('i18n dictionaries', () => {
  it('en and tr expose exactly the same key set', () => {
    const enKeys = Object.keys(dictionaries.en).sort()
    const trKeys = Object.keys(dictionaries.tr).sort()
    expect(trKeys).toEqual(enKeys)
  })

  it('every string value in both dictionaries is non-empty', () => {
    for (const lang of ['en', 'tr'] as const) {
      for (const [key, value] of Object.entries(dictionaries[lang])) {
        expect(value.length, `${lang}.${key} is empty`).toBeGreaterThan(0)
      }
    }
  })

  it('LANG defaults to tr when VITE_YUMMI_LANG is unset (test/dev default)', () => {
    // Documents the intentional asymmetry: the Makefile's own `?=` default is
    // `en` (matches the rest of the project's English-first tooling), but the
    // in-app fallback when the env var is entirely absent (bare `vitest`,
    // bare `npm run dev`) stays `tr` to match the app's pre-i18n default UI
    // language, so existing dev workflows don't silently flip language.
    expect(LANG).toBe('tr')
  })

  it('t() resolves every key to the active dictionary value', () => {
    for (const key of Object.keys(dictionaries.en) as (keyof typeof dictionaries.en)[]) {
      expect(t(key)).toBe(dictionaries[LANG][key])
    }
  })
})
