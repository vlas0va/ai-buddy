import { Link } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { PlanView } from '@/plan/PlanView'
import { useSession } from '@/store/session'

const HINT: Record<string, string> = {
  intern: 'Задачи из чек-листа адаптации по этапам. Отмечай выполненное — buddy подскажет следующий шаг.',
  mentor: 'Твои задачи наставника по чек-листу. Прогресс стажёров — в разделе «Мои стажёры».',
}

export function PlanPage() {
  const role = useSession((s) => s.user?.role)
  return (
    <section>
      <PageHeader title={role === 'mentor' ? 'План наставника' : 'План адаптации'} hint={HINT[role ?? 'intern'] ?? HINT.intern}
        actions={
          role === 'intern' ? (
            <Link to="/plan/report" className="chat-link">
              Отчёт за стажировку
            </Link>
          ) : undefined
        }
      />
      <PlanView />
    </section>
  )
}
