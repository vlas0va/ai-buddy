import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { listActions } from '@/api/actions'
import { keys } from '@/api/keys'
import { ActionProposalCard } from '@/chat/components/ActionProposalCard'
import { ActionResultCard } from '@/chat/components/ActionResultCard'
import { ChatInput } from '@/chat/components/ChatInput'
import { ErrorBlock } from '@/chat/components/ErrorBlock'
import { EscalationDraft } from '@/chat/components/EscalationDraft'
import { MessageBubble } from '@/chat/components/MessageBubble'
import { RefusalBlock } from '@/chat/components/RefusalBlock'
import { Suggestions } from '@/chat/components/Suggestions'
import { ACTION_STATUS_TEXT, ACTION_STATUS_VARIANT, STAGE_TEXT } from '@/chat/statusText'
import { useChat } from '@/chat/useChat'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'

export function ChatPage() {
  const { state, send, retry, confirm, cancel } = useChat()
  const actions = useQuery({ queryKey: keys.actions, queryFn: listActions })
  const endRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: 'end' })
  }, [state.messages, state.stage])

  const lastId = state.messages.at(-1)?.id ?? null

  return (
    <section className="chat-page">
      <PageHeader title="Чат с buddy" hint="Ответы основаны на тестовых инструкциях прототипа." />
      <div className="chat-list" aria-live="polite">
        {state.messages.length === 0 && <Suggestions onPick={send} />}
        {state.messages.map((m) => {
          const typing = state.streaming && m.id === lastId && m.author === 'assistant'
          return (
            <div key={m.id} className="chat-turn">
              <MessageBubble message={m} typing={typing} status={state.stage ? STAGE_TEXT[state.stage] : null} />
              {m.proposal && (
                <ActionProposalCard proposal={m.proposal} disabled={state.streaming} onConfirm={confirm} onCancel={(id) => void cancel(id)} />
              )}
              {m.result && <ActionResultCard result={m.result} />}
              {m.escalation && <EscalationDraft draft={m.escalation} />}
              {m.refusal && <RefusalBlock refusal={m.refusal} />}
              {m.error && (
                <ErrorBlock
                  error={m.error}
                  onRetry={m.retryText && !state.streaming ? () => retry(m.id, m.retryText as string) : undefined}
                />
              )}
            </div>
          )
        })}
        <div ref={endRef} />
      </div>
      <ChatInput disabled={state.streaming} onSend={send} />

      <div className="page-section">
        <h2>Мои заявки</h2>
        <QueryState
          isLoading={actions.isPending}
          error={actions.error}
          isEmpty={actions.data?.length === 0}
          emptyText="Заявок пока нет."
          onRetry={() => void actions.refetch()}
        >
          <ul className="list">
            {actions.data?.map((a) => (
              <li key={a.id}>
                <Card className="list-row">
                  <span>
                    {a.external_id ?? a.id}: {a.summary}
                  </span>
                  <Badge variant={ACTION_STATUS_VARIANT[a.status] ?? 'neutral'}>{ACTION_STATUS_TEXT[a.status] ?? a.status}</Badge>
                </Card>
              </li>
            ))}
          </ul>
        </QueryState>
      </div>
    </section>
  )
}
