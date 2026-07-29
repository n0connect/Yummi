import { describe, expect, it } from 'vitest'
import { sanitizeOperatorText } from './operatorText'

describe('sanitizeOperatorText', () => {
  it('removes hidden reasoning', () => {
    expect(sanitizeOperatorText('<think>private chain</think>\nOperatör yanıtı'))
      .toBe('Operatör yanıtı')
  })

  it('replaces internal protocol payloads instead of exposing them', () => {
    expect(sanitizeOperatorText('[RUN_COMMAND]\n{"command":"cat private.txt"}'))
      .toBe('İşlem durumu güncellendi.')
  })

  it('handles an unclosed reasoning block fail-closed', () => {
    expect(sanitizeOperatorText('Görünür giriş\n<think>private chain'))
      .toBe('Görünür giriş')
  })
})
