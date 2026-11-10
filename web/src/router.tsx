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
import { ApprovalsPage } from '@/pages/ApprovalsPage'
import { ChatPage } from '@/pages/ChatPage'
import { EscalationsPage } from '@/pages/EscalationsPage'
import { InternPlanPage } from '@/pages/InternPlanPage'
import { LoginPage } from '@/pages/LoginPage'
import { PlanPage } from '@/pages/PlanPage'
import { TeamPage } from '@/pages/TeamPage'
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

// only a lead decides requests; the server returns 403 for everyone else, this guard just keeps the page out of reach
const approvalsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/approvals',
  beforeLoad: () => {
    if (useSession.getState().user?.role !== 'lead') throw redirect({ to: '/chat' })
  },
  component: ApprovalsPage,
})

const teamGuard = () => {
  const role = useSession.getState().user?.role
  if (role !== 'mentor' && role !== 'lead') throw redirect({ to: '/chat' })
}

// mentor and lead: interns of the team; the server filters by team (R71), the guard only keeps others off the page
const teamRoute = createRoute({ getParentRoute: () => appRoute, path: '/team', beforeLoad: teamGuard, component: TeamPage })
const internRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/team/$internId',
  beforeLoad: teamGuard,
  component: function InternPlan() {
    const { internId } = internRoute.useParams()
    return <InternPlanPage internId={internId} />
  },
})

const questionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/questions',
  beforeLoad: () => {
    if (useSession.getState().user?.role === 'alumni') throw redirect({ to: '/chat' })
  },
  component: EscalationsPage,
})

const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([indexRoute, chatRoute, planRoute, approvalsRoute, teamRoute, internRoute, questionsRoute]),
])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
