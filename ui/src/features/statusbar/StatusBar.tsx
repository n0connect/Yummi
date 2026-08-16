import { NavLink } from 'react-router-dom'
import { t } from '../../shared/i18n/strings'

export function StatusBar(): JSX.Element {
  return (
    <div className="status-bar">
      <div className="left">
        <span className="logo">YUMMI</span>
      </div>
      <div className="right">
        <nav className="nav-seg" aria-label={t('statusBar.navAriaLabel')}>
          {[
            { to: '/', label: t('statusBar.nav.agent'), end: true },
            { to: '/terminal', label: t('statusBar.nav.bash'), end: false },
            { to: '/trace', label: t('statusBar.nav.trace'), end: false },
            { to: '/logs', label: t('statusBar.nav.logs'), end: false },
            { to: '/gateway', label: t('statusBar.nav.gateway'), end: false },
            { to: '/health', label: t('statusBar.nav.health'), end: false }
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
