import { useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import { getBenefit, type Benefit } from '@/api/plan'
import { QueryState } from '@/components/QueryState'
import { Card } from '@/components/ui/Card'
import { hours, plural } from './format'

function Meter({ percent }: { percent: number }) {
  return (
    <div className="buddy-benefit-meter" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} aria-label="Доля вопросов, закрытых buddy">
      <span style={{ width: `${percent}%` }} />
    </div>
  )
}

function Body({ b }: { b: Benefit }) {
  const marks = b.useful + b.not_useful
  if (b.asked === 0)
    return <p className="hint">Стажёры ещё не задавали вопросов buddy — здесь появится, сколько времени он сэкономил вам.</p>
  return (
    <>
      <div className="buddy-benefit-head">
        <div>
          <strong className="buddy-benefit-percent">{b.self_served_percent}%</strong>
          <span>
            вопросов стажёров buddy закрыл сам: {b.self_served} из {b.asked}
          </span>
        </div>
        <div>
          <strong className="buddy-benefit-percent">≈ {hours(b.saved_minutes)}</strong>
          <span>времени наставника сэкономлено</span>
        </div>
      </div>
      <Meter percent={b.self_served_percent} />
      <div className="report-stats">
        <div className="report-stat">
          <strong>{b.escalated}</strong>
          <span>{plural(b.escalated, 'вопрос передан', 'вопроса передано', 'вопросов передано')} наставнику</span>
        </div>
        <div className="report-stat report-stat--good">
          <strong>{b.added_to_kb}</strong>
          <span>ответов наставника в базе знаний</span>
        </div>
        <div className="report-stat report-stat--good">
          <strong>{b.useful}</strong>
          <span>«Полезно»{marks > 0 ? ` из ${marks} ${plural(marks, 'оценки', 'оценок', 'оценок')}` : ''}</span>
        </div>
        <div className="report-stat">
          <strong>{b.not_useful}</strong>
          <span>«Не полезно»</span>
        </div>
      </div>
      {b.rows.length > 1 && (
        <table className="report-table buddy-benefit-table">
          <thead>
            <tr>
              <th>Стажёр</th>
              <th>Вопросов</th>
              <th>Закрыл buddy</th>
              <th>Сэкономлено</th>
              <th>«Полезно»</th>
            </tr>
          </thead>
          <tbody>
            {b.rows.map((r) => (
              <tr key={r.user_id}>
                <td>{r.display_name}</td>
                <td>{r.asked}</td>
                <td>{r.self_served}</td>
                <td>{hours(r.saved_minutes)}</td>
                <td>{r.useful}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="hint">
        Оценка: {b.minutes_per_question} минут наставника на вопрос, который стажёр закрыл сам. Для сравнения: на наставничество за 6
        месяцев уходит 40–48 часов. Тексты вопросов здесь не показываются.
      </p>
    </>
  )
}

/** «Польза buddy» (идея 13.11): наставник и руководитель видят, какую долю вопросов их стажёров buddy закрыл сам. */
export function BenefitPanel() {
  const q = useQuery({ queryKey: keys.benefit, queryFn: getBenefit, refetchOnWindowFocus: true })
  return (
    <Card className="buddy-benefit" aria-label="Польза buddy">
      <h3>Польза buddy</h3>
      <QueryState isLoading={q.isPending} error={q.error} onRetry={() => void q.refetch()}>
        {q.data && <Body b={q.data} />}
      </QueryState>
    </Card>
  )
}
