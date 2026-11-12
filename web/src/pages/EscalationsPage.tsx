import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { errorMessage } from '@/api/client'
import { answerEscalation, listEscalations, type Escalation } from '@/api/escalations'
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

function AnswerForm({ e }: { e: Escalation }) {
  const qc = useQueryClient()
  const [text, setText] = useState('')
  const [toKb, setToKb] = useState(false)
  const answer = useMutation({
    mutationFn: () => answerEscalation(e.id, text.trim(), toKb),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.escalations }),
  })
  return (
    <div className="esc-card">
      <textarea
        className="ui-input"
        rows={3}
        maxLength={4000}
        value={text}
        onChange={(ev) => setText(ev.target.value)}
        aria-label={`Ответ для ${e.from_display_name ?? 'стажёра'}`}
        placeholder="Ваш ответ стажёру"
      />
      <label className="esc-kb">
        <input type="checkbox" checked={toKb} onChange={(ev) => setToKb(ev.target.checked)} />
        Добавить ответ в базу знаний команды (как тестовую инструкцию)
      </label>
      {toKb && (
        <p className="hint">
          Ответ увидят все стажёры команды, поэтому пишите его общим: без имён и конкретных дат. Имена и даты buddy уберёт
          сам перед добавлением.
        </p>
      )}
      {answer.isError && <p className="ui-field-error">{errorMessage(answer.error)}</p>}
      <div className="chat-actions">
        <Button size="sm" loading={answer.isPending} disabled={!text.trim()} onClick={() => answer.mutate()}>
          Ответить
        </Button>
      </div>
    </div>
  )
}

/** Questions buddy could not answer: the intern sees what they sent, mentor / lead / HR BP answer (S2 step 5, R10). */
export function EscalationsPage() {
  const role = useSession((s) => s.user?.role)
  const isIntern = role === 'intern'
  const list = useQuery({ queryKey: keys.escalations, queryFn: listEscalations, refetchOnWindowFocus: true })
  return (
    <section>
      <PageHeader
        title={isIntern ? 'Мои вопросы' : 'Вопросы стажёров'}
        hint={isIntern ? 'Вопросы, которые ты отправил людям через buddy, и ответы на них.' : 'Вопросы, на которые buddy не нашёл ответа в базе знаний.'}
      />
      <QueryState
        isLoading={list.isPending}
        error={list.error}
        isEmpty={list.data?.length === 0}
        emptyText={isIntern ? 'Ты ещё не отправлял вопросов. Если buddy не знает ответа, он подготовит черновик наставнику.' : 'Новых вопросов нет.'}
        onRetry={() => void list.refetch()}
      >
        <ul className="list">
          {list.data?.map((e) => (
            <li key={e.id}>
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
                {e.added_to_kb && <span className="hint">
                    Ответ добавлен в базу знаний команды без имён и дат — buddy будет отвечать им стажёрам твоей команды
                    {isIntern ? '' : ' (метка «Ответ наставника» под ответом)'}.
                  </span>}
                {!isIntern && e.status === 'open' && <AnswerForm e={e} />}
              </Card>
            </li>
          ))}
        </ul>
      </QueryState>
    </section>
  )
}
