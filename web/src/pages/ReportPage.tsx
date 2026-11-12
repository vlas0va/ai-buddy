import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { keys } from '@/api/keys'
import { listInterns } from '@/api/plan'
import { PageHeader } from '@/components/PageHeader'
import { ReportView } from '@/plan/ReportView'

/** Internship report of an intern for the mentor and the lead of the team (idea 12.11; not shown to the intern). */
export function ReportPage({ internId }: { internId: string }) {
  const interns = useQuery({ queryKey: keys.interns, queryFn: listInterns })
  const name = interns.data?.find((i) => i.user_id === internId)?.display_name ?? 'стажёра'
  return (
    <section>
      <PageHeader
        title={`Отчёт за стажировку: ${name}`}
        hint="Buddy собирает факты за 3 месяца: задачи плана, вопросы и доступы. Через 3 месяца придёт уведомление с итоговым отчётом."
        actions={
          <Link to="/team/$internId" params={{ internId }} className="chat-link">
            План стажёра
          </Link>
        }
      />
      <ReportView userId={internId} />
    </section>
  )
}
