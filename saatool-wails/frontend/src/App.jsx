import { useState, useEffect, useCallback } from 'react'
import { GetSettings, SaveSettings } from './api'
import Library from './pages/Library'
import Reader  from './pages/Reader'
import Settings from './pages/Settings'
import ErrorBoundary from './components/ErrorBoundary'

// ── Toast system ──────────────────────────────────────────────────────────
let _addToast = null
export function toast(msg, type = 'info') {
  if (_addToast) _addToast(msg, type)
}

function ToastContainer() {
  const [toasts, setToasts] = useState([])

  useEffect(() => {
    _addToast = (msg, type) => {
      const id = Date.now()
      setToasts(t => [...t, { id, msg, type }])
      setTimeout(() => setToasts(t => t.filter(x => x.id !== id)), 3100)
    }
    return () => { _addToast = null }
  }, [])

  return (
    <div className="toast-container">
      {toasts.map(t => (
        <div key={t.id} className={`toast ${t.type}`}>{t.msg}</div>
      ))}
    </div>
  )
}

// ── Nav tabs ──────────────────────────────────────────────────────────────
const TABS = [
  { id: 'library',  label: 'Library',  icon: '📚' },
  { id: 'settings', label: 'Settings', icon: '⚙️' },
]

export default function App() {
  const [page,    setPage]    = useState('library')
  const [theme,   setTheme]   = useState('dark')
  const [project, setProject] = useState(null)   // ProjectInfo when in reader

  // Load theme from saved settings on startup
  useEffect(() => {
    GetSettings().then(s => {
      setTheme(s.darkMode ? 'dark' : 'light')
    }).catch(() => {})
  }, [])

  // Apply theme to document
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
  }, [theme])

  const openReader = useCallback((proj) => {
    setProject(proj)
    setPage('reader')
  }, [])

  const closeReader = useCallback(() => {
    setProject(null)
    setPage('library')
  }, [])

  const toggleTheme = useCallback(() => {
    setTheme(t => {
      const next = t === 'dark' ? 'light' : 'dark'
      // Persist immediately — fire-and-forget
      GetSettings()
        .then(s => SaveSettings({ ...s, darkMode: next === 'dark' }))
        .catch(() => {})
      return next
    })
  }, [])

  // Reader is fullscreen — no bottom nav
  if (page === 'reader' && project) {
    return (
      <div className="app-shell" data-theme={theme}>
        <ErrorBoundary>
          <Reader project={project} onBack={closeReader} theme={theme} onToggleTheme={toggleTheme} />
        </ErrorBoundary>
        <ToastContainer />
      </div>
    )
  }

  return (
    <div className="app-shell" data-theme={theme}>
      <div className="app-content">
        {page === 'library'  && <ErrorBoundary><Library  onOpenReader={openReader} /></ErrorBoundary>}
        {page === 'settings' && <ErrorBoundary><Settings theme={theme} onThemeChange={setTheme} onNavigateTo={setPage} /></ErrorBoundary>}
      </div>

      <nav className="bottom-nav">
        {TABS.map(tab => (
          <button
            key={tab.id}
            className={`nav-tab ${page === tab.id ? 'active' : ''}`}
            onClick={() => setPage(tab.id)}
          >
            <span className="nav-icon">{tab.icon}</span>
            {tab.label}
          </button>
        ))}
        <div className="nav-divider" />
        <button className="theme-btn" onClick={toggleTheme} title="Toggle theme">
          <span className="nav-icon">{theme === 'dark' ? '☀️' : '🌙'}</span>
          {theme === 'dark' ? 'Light' : 'Dark'}
        </button>
      </nav>

      <ToastContainer />
    </div>
  )
}
