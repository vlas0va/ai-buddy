import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { listApprovals } from '@/api/approvals'
import { keys } from '@/api/keys'

/**
 * QA 12.11: «Мои заявки» are the user's own requests (a lead takes days off too); the requests of the team that wait
 * for the user's decision are in «Согласования» — the chat says so, so an empty «Мои заявки» does not look wrong.
 */
export function ApprovalsHint() {
  const pending = useQuery({ queryKey: keys.approvals, queryFn: listApprovals, refetchOnWindowFocus: true })
  const n = pending.data?.length ?? 0
  return (
    <div className="chat-approvals-hint">
      <p>
        {n > 0 ? (
          <>
            Ждут вашего решения: <strong>{n}</strong>
          </>
        ) : (
          'Заявок, ожидающих вашего решения, нет.'
        )}
      </p>
      <Link to="/approvals" className="chat-link">
        Открыть «Согласования»
      </Link>
      <p className="hint">Ниже — ваши собственные заявки: отгул, отпуск, командировка.</p>
    </div>
  )
}
