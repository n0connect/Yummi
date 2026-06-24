import { defineConfig } from 'vitest/config'
import path from 'path'

export default defineConfig({
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    // CSS imports (including xterm.css) are skipped in tests — they have no
    // functional value in jsdom and would otherwise cause resolution errors.
    css: false,
  },
})
