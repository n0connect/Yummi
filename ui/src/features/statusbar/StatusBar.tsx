import { NavLink } from 'react-router-dom'
import { useSystemStatus } from '../system/useSystemStatus'
import { dotClass } from '../../shared/lib/statusDot'

export function StatusBar(): JSX.Element {
  const data = useSystemStatus()

  const modules = data?.modules ?? []
  const getStatus = (name: string) =>
    modules.find(m => m.name === name)?.status ?? 'checking'

  return (
    <div className="status-bar">
      <div className="left">
        <span className="logo">YUMMI</span>
        <span className="status-separator">│</span>
        {['sandbox', 'gateway', 'orchestrator', 'conductor'].map(name => {
          const status = getStatus(name)
          return (
            <span key={name} className="status-item" title={`${name}: ${status}`}>
              <span className={dotClass(status)} />
              {name}
            </span>
          )
        })}
        {data && (
          <>
            <span className="status-separator">│</span>
            <span className="status-item" style={{ color: 'var(--text-muted)' }}>
              {data.overall}
            </span>
          </>
        )}
      </div>
      <div className="right">
        <span className="status-item status-clock" title="last health check">
          {data ? new Date(data.checked_at).toLocaleTimeString() : '—'}
        </span>
        <nav className="nav-seg">
          {[
            { to: '/', label: 'Agent', end: true },
            { to: '/terminal', label: 'Bash', end: false },
            { to: '/trace', label: 'Trace', end: false },
            { to: '/logs', label: 'Logs', end: false },
            { to: '/gateway', label: 'Gateway', end: false },
            { to: '/health', label: 'Health', end: false }
          ].map(item => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }: { isActive: boolean }) =>
                `status-nav-link${isActive ? ' active' : ''}`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </div>
    </div>
  )
}
