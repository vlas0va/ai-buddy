import { useQuery } from '@tanstack/react-query'
import { Link, Outlet, useNavigate } from '@tanstack/react-router'
import { getMe } from '@/api/auth'
import { keys } from '@/api/keys'
import { useSession } from '@/store/session'
import { ROLE_LABELS } from './roles'

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

  return (
    <div className="layout">
      <header className="layout-header">
        <strong>AI-buddy</strong>
        <nav>
          <Link to="/chat">Чат</Link>
          <Link to="/plan">План адаптации</Link>
        </nav>
        <div className="layout-user">
          {user && (
            <span>
              {user.display_name}, {ROLE_LABELS[user.role]}
            </span>
          )}
          <button type="button" onClick={logout}>
            Выйти
          </button>
        </div>
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  )
}
