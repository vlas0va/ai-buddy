import { useQuery } from '@tanstack/react-query'
import { Link, Outlet, useNavigate } from '@tanstack/react-router'
import { getMe } from '@/api/auth'
import { keys } from '@/api/keys'
import { useChatStore } from '@/store/chat'
import { useSession } from '@/store/session'
import { ROLE_LABELS } from './roles'
import { Avatar } from './ui/Avatar'
import { Button } from './ui/Button'
import { Logo } from './ui/Logo'
import './layout.css'

export function AppLayout() {
  const navigate = useNavigate()
  const clear = useSession((s) => s.clear)
  const stored = useSession((s) => s.user)
  const me = useQuery({ queryKey: keys.me, queryFn: getMe })
  const user = me.data?.user ?? stored

  function logout() {
    clear()
    void navigate({ to: '/login' })
  }

  // mock mode only: wipe the persisted demo db and the chat session pointer, then reload
  function resetDemo() {
    try {
      localStorage.removeItem('ai-buddy-mock-db')
    } catch {
      /* storage unavailable */
    }
    useChatStore.setState({ sessions: {} })
    window.location.reload()
  }

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <Logo size={28} />
          <span className="brand-name">AI-buddy</span>
        </div>
        <nav className="nav" aria-label="Основное меню">
          <Link to="/chat" className="nav-link">
            Чат
          </Link>
          <Link to="/plan" className="nav-link">
            Мой план
          </Link>
        </nav>
        <div className="sidebar-user">
          {user && (
            <div className="user-info">
              <Avatar name={user.display_name} />
              <div className="user-text">
                <span className="user-name">{user.display_name}</span>
                <span className="user-role">{ROLE_LABELS[user.role]}</span>
              </div>
            </div>
          )}
          <Button variant="ghost" size="sm" onClick={logout}>
            Выйти
          </Button>
          {import.meta.env.VITE_USE_MOCKS === 'true' && (
            <button type="button" className="reset-demo" onClick={resetDemo}>
              Сбросить демо-данные
            </button>
          )}
        </div>
      </aside>
      <main className="content">
        <Outlet />
      </main>
    </div>
  )
}
