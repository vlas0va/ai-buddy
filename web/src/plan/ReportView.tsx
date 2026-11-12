import { useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import { getReport, type Report } from '@/api/plan'
import { QueryState } from '@/components/QueryState'
import { Card } from '@/components/ui/Card'
import { shortDate } from './format'
import { PERIOD_LABELS } from './periods'

function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

/** «40 мин», «1 ч 20 мин», «3 ч» */
function hours(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  if (h === 0) return `${m} мин`
  return m === 0 ? `${h} ч` : `${h} ч ${m} мин`
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: 'good' | 'warn' | 'bad' }) {
  return (
    <div className={`report-stat${tone ? ` report-stat--${tone}` : ''}`}>
      <strong>{value}</strong>
      <span>{label}</span>
    </div>
  )
}

function Body({ r }: { r: Report }) {
  const t = r.tasks
  return (
    <div className="report">
      <p className="hint">
        {r.final ? 'Итоговый отчёт' : 'Промежуточный отчёт'}: день {Math.min(r.days, r.total_days)} из {r.total_days}
        {r.cohort_start ? `, стажировка с ${shortDate(r.cohort_start)}` : ''}. Это факты из плана и чата, а не оценка —
        выводы делает наставник. Отчёт видят только наставник и руководитель.
      </p>

      <Card className="report-card" aria-label="Задачи плана">
        <h3>Задачи плана: выполнено {t.done} из {t.total}</h3>
        <div className="report-stats">
          <Stat label="раньше срока" value={t.early} tone="good" />
          <Stat label="в срок" value={t.on_time} tone="good" />
          <Stat label="с опозданием" value={t.late} tone="warn" />
          <Stat label="просрочено сейчас" value={t.overdue_now} tone="bad" />
          <Stat label="срок ещё не наступил" value={t.upcoming} />
        </div>
        <table className="report-table">
          <thead>
            <tr>
              <th>Этап</th>
              <th>Выполнено</th>
              <th>С опозданием</th>
            </tr>
          </thead>
          <tbody>
            {r.periods.map((p) => (
              <tr key={p.period}>
                <td>{PERIOD_LABELS[p.period] ?? p.period}</td>
                <td>
                  {p.done} из {p.total}
                </td>
                <td>{p.late}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {r.overdue.length > 0 && (
          <>
            <h4>Просрочено сейчас</h4>
            <ul>
              {r.overdue.map((x) => (
                <li key={x.title}>
                  {x.title} — срок {shortDate(x.due_at)}
                </li>
              ))}
            </ul>
          </>
        )}
        {r.late.length > 0 && (
          <>
            <h4>Выполнено с опозданием</h4>
            <ul>
              {r.late.map((x) => (
                <li key={x.title}>
                  {x.title} — срок {shortDate(x.due_at)}, выполнено {shortDate(x.completed_at)}
                </li>
              ))}
            </ul>
          </>
        )}
      </Card>

      <Card className="report-card report-saved" aria-label="Время наставника">
        <h3>Время наставника</h3>
        <p className="report-saved-main">
          <strong>≈ {hours(r.questions.saved_minutes)}</strong> сэкономил buddy: {r.questions.self_served}{' '}
          {plural(r.questions.self_served, 'вопрос', 'вопроса', 'вопросов')} стажёр закрыл сам, без наставника.
        </p>
        <p className="hint">
          Оценка: 10 минут наставника на вопрос. Для сравнения: на наставничество за 6 месяцев стажировки уходит 40–48 часов.
        </p>
      </Card>

      <Card className="report-card" aria-label="Вопросы и доступы">
        <h3>Вопросы и доступы</h3>
        <div className="report-stats">
          <Stat label="вопросов buddy" value={r.questions.asked} />
          <Stat label="передано наставнику" value={r.questions.escalated} />
          <Stat label="получили ответ" value={r.questions.answered} />
          <Stat label="ответов добавлено в базу знаний" value={r.questions.added_to_kb} tone="good" />
          <Stat label="заявок на доступы" value={r.accesses_requested} />
          <Stat label="целей на стажировку" value={r.goals} />
        </div>
      </Card>

      <Card className="report-card" aria-label="Что обсудить на 1:1">
        <h3>Что обсудить на 1:1</h3>
        <ul>
          {r.discuss.map((d) => (
            <li key={d}>{d}</li>
          ))}
        </ul>
      </Card>
    </div>
  )
}

/** Internship report of an intern (idea 12.11), for the mentor and the lead of the team. */
export function ReportView({ userId }: { userId: string }) {
  const q = useQuery({ queryKey: keys.report(userId), queryFn: () => getReport(userId) })
  return (
    <QueryState isLoading={q.isLoading} error={q.error} onRetry={() => void q.refetch()}>
      {q.data && <Body r={q.data} />}
    </QueryState>
  )
}
