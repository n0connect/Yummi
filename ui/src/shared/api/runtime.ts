// Same-origin relative requests, unless VITE_YUMMI_API_ORIGIN overrides it
// (e.g. serving the UI from a different origin than the orchestrator).
const configuredOrigin = (import.meta.env.VITE_YUMMI_API_ORIGIN || '').trim().replace(/\/$/, '')

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
