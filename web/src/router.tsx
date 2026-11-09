import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  useNavigate,
  Outlet,
  type RouterHistory,
} from '@tanstack/react-router'
import { AppLayout } from '@/components/AppLayout'
import { ChatPage } from '@/pages/ChatPage'
import { LoginPage } from '@/pages/LoginPage'
import { PlanPage } from '@/pages/PlanPage'
import { useSession } from '@/store/session'

const rootRoute = createRootRoute({ component: Outlet })

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: () => {
    if (useSession.getState().token) throw redirect({ to: '/chat' })
  },
  component: function Login() {
    const navigate = useNavigate()
    return <LoginPage onLoggedIn={() => void navigate({ to: '/chat' })} />
  },
})

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  beforeLoad: () => {
    if (!useSession.getState().token) throw redirect({ to: '/login' })
  },
  component: AppLayout,
})

const indexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/chat' })
  },
})

const chatRoute = createRoute({ getParentRoute: () => appRoute, path: '/chat', component: ChatPage })
const planRoute = createRoute({ getParentRoute: () => appRoute, path: '/plan', component: PlanPage })

const routeTree = rootRoute.addChildren([loginRoute, appRoute.addChildren([indexRoute, chatRoute, planRoute])])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
