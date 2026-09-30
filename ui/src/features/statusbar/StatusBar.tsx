import { NavLink } from 'react-router-dom'
import { t } from '../../shared/i18n/strings'
import { useTheme } from '../../shared/lib/useTheme'

/** Sun (light active) / moon (dark active) — mirrors SessionRail's inline
 * icon convention: 20x20 viewBox, stroke-only, currentColor. */
function ThemeIcon({ theme }: { theme: 'light' | 'dark' }): JSX.Element {
  return (
    <svg
      className="theme-toggle-icon"
      viewBox="0 0 20 20"
      fill="none"
      aria-hidden="true"
    >
      {theme === 'dark' ? (
        <path
          d="M16.5 12.4A7 7 0 0 1 7.6 3.5a7 7 0 1 0 8.9 8.9Z"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
      ) : (
        <>
          <circle cx="10" cy="10" r="3.5" stroke="currentColor" strokeWidth="1.5" />
          <path
            d="M10 2.75v2M10 15.25v2M17.25 10h-2M4.75 10h-2M15.13 4.87l-1.41 1.41M6.28 13.72l-1.41 1.41M15.13 15.13l-1.41-1.41M6.28 6.28L4.87 4.87"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
          />
        </>
      )}
    </svg>
  )
}

export function StatusBar(): JSX.Element {
  const { theme, toggleTheme } = useTheme()
  const themeToggleLabel = theme === 'dark' ? t('statusBar.theme.switchToLight') : t('statusBar.theme.switchToDark')

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
            { to: '/desktop', label: t('statusBar.nav.desktop'), end: false },
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
        <button
          type="button"
          className="theme-toggle-btn"
          onClick={toggleTheme}
          title={themeToggleLabel}
          aria-label={themeToggleLabel}
        >
          <ThemeIcon theme={theme} />
        </button>
      </div>
    </div>
  )
}
