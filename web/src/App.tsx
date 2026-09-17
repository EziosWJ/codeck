import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import Dashboard from './pages/Dashboard'
import Chat from './pages/Chat'
import Profiles from './pages/Profiles'
import Tasks from './pages/Tasks'
import History from './pages/History'
import Usage from './pages/Usage'

const NAV = [
  { to: '/', label: '总览', end: true },
  { to: '/chat', label: '对话', end: false },
  { to: '/profiles', label: '档案', end: false },
  { to: '/tasks', label: '任务', end: false },
  { to: '/usage', label: '用量', end: false },
  { to: '/history', label: '历史', end: false },
]

export default function App() {
  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-name">Codeck</div>
          <div className="brand-sub">本机 Codex 控制台</div>
        </div>
        <nav className="nav">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => (isActive ? 'nav-item active' : 'nav-item')}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">
          本地管理控制台。
          <br />
          通过 /api 连接后端。
        </div>
      </aside>

      <main className="main">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/chat" element={<Chat />} />
          <Route path="/profiles" element={<Profiles />} />
          <Route path="/tasks" element={<Tasks />} />
          <Route path="/usage" element={<Usage />} />
          <Route path="/history" element={<History />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}
