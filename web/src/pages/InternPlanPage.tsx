import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { keys } from '@/api/keys'
import { listInterns } from '@/api/plan'
import { PageHeader } from '@/components/PageHeader'
import { PlanView } from '@/plan/PlanView'

/** A mentor or lead looks at one intern's plan (read-only tasks) and sets adaptation goals. */
export function InternPlanPage({ internId }: { internId: string }) {
  const interns = useQuery({ queryKey: keys.interns, queryFn: listInterns })
  const name = interns.data?.find((i) => i.user_id === internId)?.display_name ?? 'стажёра'
  return (
    <section>
      <PageHeader
        title={`План: ${name}`}
        hint="Задачи отмечает сам стажёр. Вы задаёте цели и видите, что просрочено."
        actions={
          <span className="page-links">
            <Link to="/team/$internId/report" params={{ internId }} className="chat-link">
              Отчёт за стажировку
            </Link>
            <Link to="/team" className="chat-link">
              Все стажёры
            </Link>
          </span>
        }
      />
      <PlanView userId={internId} canEditGoals />
    </section>
  )
}
