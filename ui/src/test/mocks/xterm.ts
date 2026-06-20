// Stub for @xterm/xterm — used in vitest (jsdom) environment only.
// Production code uses the real xterm.js from node_modules.
import { vi } from 'vitest'

export class Terminal {
  cols = 80
  rows = 24
  element: HTMLElement | undefined

  open(container: HTMLElement) {
    this.element = container
  }
  write = vi.fn()
  onData = vi.fn((_cb: (data: string) => void) => ({ dispose: vi.fn() }))
  loadAddon = vi.fn()
  clear = vi.fn()
  dispose = vi.fn()
}
