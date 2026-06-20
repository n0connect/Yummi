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

// Mock EventSource for SSE tests
const EventSourceMock = Object.assign(
  vi.fn().mockImplementation(() => ({
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    close: vi.fn(),
    readyState: 0,
    url: '',
    onopen: null,
    onmessage: null,
    onerror: null,
    CONNECTING: 0,
    OPEN: 1,
    CLOSED: 2,
  })),
  { CONNECTING: 0 as const, OPEN: 1 as const, CLOSED: 2 as const },
)

globalThis.EventSource = EventSourceMock as unknown as typeof EventSource
