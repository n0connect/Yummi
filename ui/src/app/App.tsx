import { lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, Outlet } from 'react-router-dom'
import './app.css'
import { StatusBar } from '../features/statusbar/StatusBar'
import { ChatPage } from '../pages/chat/ChatPage'
import { t } from '../shared/i18n/strings'

const TerminalPage = lazy(() => import('../pages/terminal/TerminalPage').then(module => ({ default: module.TerminalPage })))
const TracePage = lazy(() => import('../pages/trace/TracePage').then(module => ({ default: module.TracePage })))
const LogsPage = lazy(() => import('../pages/logs/LogsPage').then(module => ({ default: module.LogsPage })))
const GatewayPage = lazy(() => import('../pages/gateway/GatewayPage').then(module => ({ default: module.GatewayPage })))
const HealthPage = lazy(() => import('../pages/health/HealthPage').then(module => ({ default: module.HealthPage })))

function Layout() {
  return (
    <>
      <StatusBar />
      <Suspense fallback={<div className="route-loading" role="status" aria-live="polite">{t('app.routeLoading')}</div>}>
        <Outlet />
      </Suspense>
    </>
  )
}

export function App(): JSX.Element {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<ChatPage />} />
          <Route path="/terminal" element={<TerminalPage />} />
          <Route path="/trace" element={<TracePage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/gateway" element={<GatewayPage />} />
          <Route path="/health" element={<HealthPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
