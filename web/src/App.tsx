import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import Dashboard from './pages/Dashboard'
import Chat from './pages/Chat'
import Profiles from './pages/Profiles'
import Tasks from './pages/Tasks'
import History from './pages/History'
import Usage from './pages/Usage'
import Timeline from './pages/Timeline'

const NAV = [
  { to: '/', label: '总览', end: true },
  { to: '/chat', label: '对话', end: false },
  { to: '/profiles', label: '档案', end: false },
  { to: '/tasks', label: '任务', end: false },
  { to: '/timeline', label: '重置时间线', end: false },
  { to: '/usage', label: '用量', end: false },
  { to: '/history', label: '历史', end: false },
]

const GITHUB_URL = 'https://github.com/EziosWJ/codeck'

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
          <br />
          <a
            className="github-link"
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer"
            title={GITHUB_URL}
          >
            <svg
              className="github-icon"
              viewBox="0 0 16 16"
              width="12"
              height="12"
              aria-hidden="true"
            >
              <path
                fill="currentColor"
                d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
              />
            </svg>
            GitHub 仓库
          </a>
        </div>
      </aside>

      <main className="main">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/chat" element={<Chat />} />
          <Route path="/profiles" element={<Profiles />} />
          <Route path="/tasks" element={<Tasks />} />
          <Route path="/timeline" element={<Timeline />} />
          <Route path="/usage" element={<Usage />} />
          <Route path="/history" element={<History />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}
