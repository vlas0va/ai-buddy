import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { errorMessage } from '@/api/client'
import { answerEscalation, listEscalations, listQueue, type Escalation, type QueueGroup } from '@/api/escalations'
import { keys } from '@/api/keys'
import { ROLE_TEXT } from '@/chat/statusText'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { useSession } from '@/store/session'
import '@/plan/plan.css'

function when(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function AnswerForm({ ids, asker }: { ids: string[]; asker: string }) {
  const qc = useQueryClient()
  const [text, setText] = useState('')
  const [toKb, setToKb] = useState(true) // организаторы 13.11: список нужен, чтобы наполнять базу знаний
  const [scope, setScope] = useState<'team' | 'all'>('team')
  const [first, ...rest] = ids
  const answer = useMutation({
    mutationFn: () => answerEscalation(first ?? '', text.trim(), toKb, scope, rest),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.queue })
      void qc.invalidateQueries({ queryKey: keys.escalations })
    },
  })
  const e = { id: first ?? '' }
  return (
    <div className="esc-card">
      <textarea
        className="ui-input"
        rows={3}
        maxLength={4000}
        value={text}
        onChange={(ev) => setText(ev.target.value)}
        aria-label={`Ответ для ${asker}`}
        placeholder="Ваш ответ стажёру"
      />
      <label className="esc-kb">
        <input type="checkbox" checked={toKb} onChange={(ev) => setToKb(ev.target.checked)} />
        Добавить ответ в базу знаний (как тестовую инструкцию)
      </label>
      {toKb && (
        <>
          <div className="esc-scope" role="radiogroup" aria-label="Кому виден ответ">
            <label>
              <input type="radio" name={`scope-${e.id}`} checked={scope === 'team'} onChange={() => setScope('team')} />
              Стажёрам моей команды — про наши сервисы и процессы
            </label>
            <label>
              <input type="radio" name={`scope-${e.id}`} checked={scope === 'all'} onChange={() => setScope('all')} />
              Всем стажёрам — общий вопрос (офис, документы, правила)
            </label>
          </div>
          <p className="hint">
            Пишите ответ общим: без имён и конкретных дат — buddy уберёт их сам перед добавлением. На такой же вопрос buddy
            дальше ответит сам, с пометкой «Ответ наставника».
          </p>
        </>
      )}
      {answer.isError && <p className="ui-field-error">{errorMessage(answer.error)}</p>}
      <div className="chat-actions">
        <Button size="sm" loading={answer.isPending} disabled={!text.trim()} onClick={() => answer.mutate()}>
          {ids.length > 1 ? `Ответить всем (${ids.length})` : 'Ответить'}
        </Button>
      </div>
    </div>
  )
}

function plural(n: number) {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return 'раз'
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return 'раза'
  return 'раз'
}

function QueueCard({ g }: { g: QueueGroup }) {
  return (
    <Card className="esc-card" aria-label={`Вопрос: ${g.question}`}>
      <div className="esc-head">
        <strong>{g.askers.join(', ')}</strong>
        <span className="team-meta">
          <span>{when(g.last_asked_at)}</span>
          {g.count > 1 && <Badge variant="in_progress">Спросили {g.count} {plural(g.count)}</Badge>}
          <Badge variant="waiting_approval">Ждёт ответа</Badge>
        </span>
      </div>
      <p className="esc-text">{g.question}</p>
      <AnswerForm ids={g.ids} asker={g.askers.join(', ')} />
    </Card>
  )
}

function EscalationCard({ e, isIntern }: { e: Escalation; isIntern: boolean }) {
  return (
    <Card className="esc-card" aria-label={`Вопрос от ${e.from_display_name ?? ''}`}>
      <div className="esc-head">
        <strong>{isIntern ? `Кому: ${ROLE_TEXT[e.to_role] ?? e.to_role}` : e.from_display_name}</strong>
        <span className="team-meta">
          <span>{when(e.created_at)}</span>
          <Badge variant={e.status === 'answered' ? 'done' : 'waiting_approval'}>{e.status === 'answered' ? 'Есть ответ' : 'Ждёт ответа'}</Badge>
        </span>
      </div>
      <p className="esc-text">{e.text}</p>
      {e.answer && <p className="esc-answer">{e.answer}</p>}
      {e.added_to_kb && (
        <span className="hint">
          Ответ добавлен в базу знаний без имён и дат — на такой же вопрос buddy дальше ответит сам, с пометкой «Ответ наставника».
        </span>
      )}
    </Card>
  )
}

/** Вопросы стажёра, на которые buddy не ответил, и ответы на них. */
function MyQuestions() {
  const list = useQuery({ queryKey: keys.escalations, queryFn: listEscalations, refetchOnWindowFocus: true })
  return (
    <section>
      <PageHeader title="Мои вопросы" hint="Вопросы, на которые buddy не нашёл ответа: они в списке наставника, ответ появится здесь." />
      <QueryState
        isLoading={list.isPending}
        error={list.error}
        isEmpty={list.data?.length === 0}
        emptyText="Пока пусто. Если buddy не знает ответа, вопрос попадёт сюда и в список наставника."
        onRetry={() => void list.refetch()}
      >
        <ul className="list">
          {list.data?.map((e) => (
            <li key={e.id}>
              <EscalationCard e={e} isIntern />
            </li>
          ))}
        </ul>
      </QueryState>
    </section>
  )
}

/**
 * Организаторы 13.11: наставник (руководитель, HR BP) отвечает на список пачкой, когда удобно — похожие вопросы собраны
 * в группу, один ответ закрывает группу, уведомляет каждого, кто спрашивал, и уходит в базу знаний.
 */
function QuestionStack() {
  const queue = useQuery({ queryKey: keys.queue, queryFn: listQueue, refetchOnWindowFocus: true })
  const list = useQuery({ queryKey: keys.escalations, queryFn: listEscalations, refetchOnWindowFocus: true })
  const answered = (list.data ?? []).filter((e) => e.status === 'answered')
  return (
    <section>
      <PageHeader
        title="Вопросы стажёров"
        hint="Вопросы, на которые buddy не нашёл ответа. Стажёр пишет вам сам в личные сообщения или в канал, а здесь вопросы копятся — ответьте, когда удобно: ответ попадёт в базу знаний, и buddy дальше ответит сам."
      />
      <QueryState
        isLoading={queue.isPending}
        error={queue.error}
        isEmpty={queue.data?.length === 0}
        emptyText="Новых вопросов нет."
        onRetry={() => void queue.refetch()}
      >
        <ul className="list">
          {queue.data?.map((g) => (
            <li key={g.ids[0]}>
              <QueueCard g={g} />
            </li>
          ))}
        </ul>
      </QueryState>
      {answered.length > 0 && (
        <>
          <h2 className="esc-section">Отвечено</h2>
          <ul className="list">
            {answered.map((e) => (
              <li key={e.id}>
                <EscalationCard e={e} isIntern={false} />
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  )
}

/**
 * Вопросы, на которые buddy не ответил: стажёр видит свои, наставник / руководитель / HR BP отвечают на свой список
 * (S2, R10).
 */
export function EscalationsPage() {
  const role = useSession((s) => s.user?.role)
  return role === 'intern' ? <MyQuestions /> : <QuestionStack />
}
