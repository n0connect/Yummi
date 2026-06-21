// Shared status → indicator-dot class mapping. Used by StatusBar (infra
// services) and StatusBadge (module cards) so the two stay in sync.
export function dotClass(status: string): string {
  switch (status) {
    case 'healthy':       return 'dot dot-ok'
    case 'degraded':      return 'dot dot-warn'
    case 'down':          return 'dot dot-err'
    case 'not_connected': return 'dot dot-muted'
    default:              return 'dot dot-dim'
  }
}
