// Browser/Compose builds keep same-origin relative requests. The desktop build
// sets VITE_YUMMI_API_ORIGIN, so the identical React bundle talks directly to
// the loopback-only orchestrator instead of relying on nginx at localhost:3000.
export const isDesktopShell = typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window
const configuredOrigin = (
  import.meta.env.VITE_YUMMI_API_ORIGIN || (isDesktopShell ? 'http://127.0.0.1:9080' : '')
).trim().replace(/\/$/, '')

export function apiURL(path: string): string {
  if (!configuredOrigin) return path
  return new URL(path, `${configuredOrigin}/`).toString()
}

export function websocketURL(path: string): string {
  if (configuredOrigin) {
    const url = new URL(path, `${configuredOrigin}/`)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    return url.toString()
  }
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}${path}`
}
