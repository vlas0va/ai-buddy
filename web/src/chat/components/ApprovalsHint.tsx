import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { listApprovals } from '@/api/approvals'
import { keys } from '@/api/keys'

/**
 * QA 12.11: «Мои заявки» — собственные заявки пользователя (руководитель тоже берёт отгулы); заявки команды, ждущие
 * решения пользователя, — в «Согласованиях»; чат об этом говорит, чтобы пустые «Мои заявки» не выглядели ошибкой.
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
