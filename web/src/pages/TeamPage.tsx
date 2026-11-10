import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { keys } from '@/api/keys'
import { listInterns } from '@/api/plan'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Avatar } from '@/components/ui/Avatar'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import { PERIOD_LABELS } from '@/plan/periods'
import '@/plan/plan.css'

/** Mentor and lead: interns of their team with progress (R08, R11). Data comes filtered by core (R71). */
export function TeamPage() {
  const interns = useQuery({ queryKey: keys.interns, queryFn: listInterns, refetchOnWindowFocus: true })
  return (
    <section>
      <PageHeader title="Мои стажёры" hint="Прогресс по плану адаптации. Откройте стажёра, чтобы увидеть задачи и задать цели." />
      <QueryState
        isLoading={interns.isPending}
        error={interns.error}
        isEmpty={interns.data?.length === 0}
        emptyText="Стажёров в команде пока нет."
        onRetry={() => void interns.refetch()}
      >
        <ul className="list">
          {interns.data?.map((i) => (
            <li key={i.user_id}>
              <Link to="/team/$internId" params={{ internId: i.user_id }} className="ui-card team-row" aria-label={`План: ${i.display_name}`}>
                <div className="user-info">
                  <Avatar name={i.display_name} />
                  <div className="user-text">
                    <strong>{i.display_name}</strong>
                    <span className="team-meta">
                      <span>Этап: {PERIOD_LABELS[i.current_period]}</span>
                      <span>
                        Готово {i.done} из {i.total}
                      </span>
                      {(i.pending_access_requests ?? 0) > 0 && <span>Запросов доступа: {i.pending_access_requests}</span>}
                    </span>
                  </div>
                </div>
                {i.overdue > 0 ? <Badge variant="overdue">Просрочено: {i.overdue}</Badge> : <Badge variant="done">В графике</Badge>}
              </Link>
            </li>
          ))}
        </ul>
      </QueryState>
      <Card className="team-hint">
        <p className="hint">Buddy напоминает стажёрам о задачах этапа, но встречи 1:1, обратную связь и экватор проводите вы.</p>
      </Card>
    </section>
  )
}
