import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { keys } from '@/api/keys'
import { listInterns } from '@/api/plan'
import { PageHeader } from '@/components/PageHeader'
import { ReportView } from '@/plan/ReportView'

function InternReportHeader({ internId }: { internId: string }) {
  const interns = useQuery({ queryKey: keys.interns, queryFn: listInterns })
  const name = interns.data?.find((i) => i.user_id === internId)?.display_name ?? 'стажёра'
  return (
    <PageHeader
      title={`Отчёт за стажировку: ${name}`}
      hint="Buddy собирает факты за 3 месяца: задачи плана, вопросы и доступы. Через 3 месяца придёт уведомление с итоговым отчётом."
      actions={
        <Link to="/team/$internId" params={{ internId }} className="chat-link">
          План стажёра
        </Link>
      }
    />
  )
}

/** Internship report page (idea 12.11): an intern's own report, or an intern's report for the mentor and the lead. */
export function ReportPage({ internId }: { internId?: string }) {
  return (
    <section>
      {internId ? (
        <InternReportHeader internId={internId} />
      ) : (
        <PageHeader
          title="Мой отчёт за стажировку"
          hint="Факты за 3 месяца: задачи плана, вопросы и доступы. Через 3 месяца придёт уведомление с итоговым отчётом."
          actions={
            <Link to="/plan" className="chat-link">
              Мой план
            </Link>
          }
        />
      )}
      <ReportView userId={internId} />
    </section>
  )
}
