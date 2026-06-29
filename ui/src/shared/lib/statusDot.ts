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

// Allowlist of known module-status values to a fixed CSS suffix — status
// comes from the orchestrator/backend, so this is a closed-set lookup, never
// a derived/interpolated className built from the string itself (which would
// let an unanticipated status value inject an arbitrary class name).
export function badgeClass(status: string): string {
  switch (status) {
    case 'healthy':       return 'status-badge badge-healthy'
    case 'degraded':      return 'status-badge badge-degraded'
    case 'down':          return 'status-badge badge-down'
    case 'not_connected': return 'status-badge badge-not_connected'
    default:              return 'status-badge badge-unknown'
  }
}
