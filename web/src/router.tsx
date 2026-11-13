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
import { ReportPage } from '@/pages/ReportPage'
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

// решения по заявкам принимают только руководитель и HR-администратор (второй этап отсутствия); остальным сервер
// отвечает 403, эта защита лишь не пускает на страницу
const approvalsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/approvals',
  beforeLoad: () => {
    const role = useSession.getState().user?.role
    if (role !== 'lead' && role !== 'hr_admin') throw redirect({ to: '/chat' })
  },
  component: ApprovalsPage,
})

const teamGuard = () => {
  const role = useSession.getState().user?.role
  if (role !== 'mentor' && role !== 'lead') throw redirect({ to: '/chat' })
}

// наставник и руководитель: стажёры команды; сервер фильтрует по команде (R71), защита лишь не пускает остальных на
// страницу
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

// Отчёт за стажировку (идея 12.11): только для наставника и руководителя команды.
const internReportRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/team/$internId/report',
  beforeLoad: teamGuard,
  component: function InternReport() {
    const { internId } = internReportRoute.useParams()
    return <ReportPage internId={internId} />
  },
})

const questionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/questions',
  beforeLoad: () => {
    const role = useSession.getState().user?.role
    if (role === 'alumni' || role === 'hr_admin') throw redirect({ to: '/chat' })
  },
  component: EscalationsPage,
})

const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([indexRoute, chatRoute, planRoute, approvalsRoute, teamRoute, internRoute, internReportRoute, questionsRoute]),
])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
