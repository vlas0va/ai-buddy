import { useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import { getPlan } from '@/api/plan'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { STATUS_LABELS } from '@/components/ui/badgeText'
import { Card } from '@/components/ui/Card'

// Lists periods and tasks with status badges. Task actions come later.
export function PlanPage() {
  const plan = useQuery({ queryKey: keys.plan(), queryFn: () => getPlan() })
  const periods = plan.data?.periods ?? []

  return (
    <section>
      <PageHeader title="План адаптации" />
      <QueryState
        isLoading={plan.isPending}
        error={plan.error}
        isEmpty={periods.length === 0}
        emptyText="План пока пуст."
        onRetry={() => void plan.refetch()}
      >
        {periods.map((p) => (
          <div key={p.id} className="page-section">
            <h2>{p.label}</h2>
            <ul className="list">
              {p.tasks.map((t) => (
                <li key={t.id}>
                  <Card className="list-row">
                    <span>{t.title}</span>
                    <Badge variant={t.status}>{STATUS_LABELS[t.status]}</Badge>
                  </Card>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </QueryState>
    </section>
  )
}
