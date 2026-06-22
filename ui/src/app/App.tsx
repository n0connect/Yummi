import { BrowserRouter, Routes, Route, Outlet } from 'react-router-dom'
import './app.css'
import { StatusBar } from '../features/statusbar/StatusBar'
import { ChatPage } from '../pages/chat/ChatPage'
import { TerminalPage } from '../pages/terminal/TerminalPage'
import { LogsPage } from '../pages/logs/LogsPage'
import { GatewayPage } from '../pages/gateway/GatewayPage'
import { HealthPage } from '../pages/health/HealthPage'

function Layout() {
  return (
    <>
      <StatusBar />
      <Outlet />
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
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/gateway" element={<GatewayPage />} />
          <Route path="/health" element={<HealthPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
