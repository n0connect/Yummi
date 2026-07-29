import { NavLink } from 'react-router-dom'
import { isDesktopShell } from '../../shared/api/runtime'

export function StatusBar(): JSX.Element {
  return (
    <div className="status-bar">
      <div className="left">
        <span className="logo">YUMMI</span>
        {isDesktopShell && <span className="desktop-badge">DESKTOP</span>}
      </div>
      <div className="right">
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
