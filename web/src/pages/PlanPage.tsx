import { useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import { getPlan } from '@/api/plan'
import { QueryState } from '@/components/QueryState'

// Empty shell: lists periods and task titles only. Task statuses and actions come later.
export function PlanPage() {
  const plan = useQuery({ queryKey: keys.plan(), queryFn: () => getPlan() })
  const periods = plan.data?.periods ?? []

  return (
    <section>
      <h1>План адаптации</h1>
      <QueryState
        isLoading={plan.isPending}
        error={plan.error}
        isEmpty={periods.length === 0}
        emptyText="План пока пуст."
        onRetry={() => void plan.refetch()}
      >
        {periods.map((p) => (
          <div key={p.id}>
            <h2>{p.label}</h2>
            <ul>
              {p.tasks.map((t) => (
                <li key={t.id}>{t.title}</li>
              ))}
            </ul>
          </div>
        ))}
      </QueryState>
    </section>
  )
}
