import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `make dev-ui` runs this standalone against an already-running orchestrator
// (docker-compose or `make dev-orchestrator`, both on :9080). Without this
// proxy, relative /api and /ws calls hit the Vite dev server itself instead
// of the orchestrator — the container build doesn't need it (nginx proxies
// there instead), but the bare dev server has nothing else providing it.
export default defineConfig({
  plugins: [react()],
  build: {
    // Vite's own default target (chrome87/es2020/safari14/...) predates
    // top-level await, which @novnc/novnc's own source uses (a WebCodecs
    // H264 support probe) — not a deliberate legacy-browser commitment this
    // project ever made, just the unconfigured default. Yummi is a
    // single-operator local tool served from its own bundled origin, so
    // there's no real audience running a browser this old; 'esnext' targets
    // whatever the browser running the build's own toolchain supports.
    target: 'esnext',
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:9080', changeOrigin: true },
      '/ws': { target: 'http://127.0.0.1:9080', ws: true, changeOrigin: true },
    },
  },
})
