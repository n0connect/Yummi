import { describe, expect, it } from 'vitest'
import { deriveTrustState } from './operatorTrust'

describe('deriveTrustState', () => {
  it('marks the control plane healthy only when events and kernel enforcement agree', () => {
    expect(deriveTrustState('live', { status: 'connected', rules_active: true, policy_kernel_consistent: true })).toBe('trusted')
    expect(deriveTrustState('recovering', { status: 'connected', rules_active: true, policy_kernel_consistent: true })).toBe('recovering')
    expect(deriveTrustState('live', { status: 'connected', rules_active: true, policy_kernel_consistent: false })).toBe('attention')
    expect(deriveTrustState('live', { status: 'disconnected', rules_active: false, policy_kernel_consistent: false })).toBe('offline')
  })
})
