import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `make dev-ui` runs this standalone against an already-running orchestrator
// (docker-compose or `make dev-orchestrator`, both on :9080). Without this
// proxy, relative /api and /ws calls hit the Vite dev server itself instead
// of the orchestrator — the container build doesn't need it (nginx proxies
// there instead), but the bare dev server has nothing else providing it.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:9080', changeOrigin: true },
      '/ws': { target: 'http://127.0.0.1:9080', ws: true, changeOrigin: true },
    },
  },
})
