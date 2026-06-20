import { defineConfig } from 'vitest/config'
import path from 'path'

export default defineConfig({
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    // xterm.js requires real DOM canvas/WebGL which jsdom doesn't provide.
    // These aliases redirect xterm imports to lightweight stubs during tests only.
    // Production builds use the real packages from node_modules.
    alias: {
      // Subpath alias must come BEFORE the package alias (more specific first).
      '@xterm/xterm/css/xterm.css': path.resolve(__dirname, 'src/test/mocks/empty.css'),
      '@xterm/xterm': path.resolve(__dirname, 'src/test/mocks/xterm.ts'),
      '@xterm/addon-fit': path.resolve(__dirname, 'src/test/mocks/addon-fit.ts'),
      '@xterm/addon-web-links': path.resolve(__dirname, 'src/test/mocks/addon-web-links.ts'),
    },
    // CSS imports (including xterm.css) are skipped in tests — they have no
    // functional value in jsdom and would otherwise cause resolution errors.
    css: false,
  },
})
