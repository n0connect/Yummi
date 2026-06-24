import { vi } from 'vitest'

// jsdom doesn't implement scrollIntoView — stub it globally so components
// that call ref.scrollIntoView() don't throw in tests.
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = vi.fn()
}

// jsdom 25 includes ResizeObserver but some environments may not. Stub it so
// TerminalPanel's ResizeObserver usage doesn't crash during rendering.
if (typeof ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe = vi.fn()
    unobserve = vi.fn()
    disconnect = vi.fn()
  } as unknown as typeof ResizeObserver
}

// No mocks allowed. EventSource tests must run against real E2E environment or be designed without mocks.
