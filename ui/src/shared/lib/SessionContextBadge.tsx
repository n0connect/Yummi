import { useActiveSessionLabel } from './useActiveSessionLabel'
import { t } from '../i18n/strings'
import './SessionContextBadge.css'

// Shown on the Bash and Desktop pages: the SAME session the operator was
// just working in on the Agent tab, so switching tabs reads as moving
// around one workspace rather than stepping into a disconnected tool.
export function SessionContextBadge(): JSX.Element | null {
  const label = useActiveSessionLabel()
  if (!label) return null
  return (
    <div className="session-context-badge">
      <span className="session-context-label">{t('common.sessionLabel')}</span>
      <span className="session-context-value">{label}</span>
    </div>
  )
}
