import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { listActions } from '@/api/actions'
import { keys } from '@/api/keys'
import { ActionProposalCard } from '@/chat/components/ActionProposalCard'
import { ActionResultCard } from '@/chat/components/ActionResultCard'
import { ActionsPanel } from '@/chat/components/ActionsPanel'
import { ChatInput } from '@/chat/components/ChatInput'
import { ErrorBlock } from '@/chat/components/ErrorBlock'
import { EscalationDraft } from '@/chat/components/EscalationDraft'
import { MessageBubble, WelcomeBubble } from '@/chat/components/MessageBubble'
import { RefusalBlock } from '@/chat/components/RefusalBlock'
import { RequestsDrawer } from '@/chat/components/RequestsDrawer'
import { STAGE_TEXT } from '@/chat/statusText'
import { useChat } from '@/chat/useChat'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/Button'
import { useSession } from '@/store/session'

export function ChatPage() {
  const { state, restoring, send, retry, confirm, cancel, newDialog } = useChat()
  const actions = useQuery({ queryKey: keys.actions, queryFn: listActions })
  const user = useSession((s) => s.user)
  const [drawer, setDrawer] = useState(false)
  const closeDrawer = useCallback(() => setDrawer(false), [])
  const endRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: 'end' })
  }, [state.messages, state.stage])

  const lastId = state.messages.at(-1)?.id ?? null
  const firstName = user?.display_name.split(' ')[0] ?? ''

  return (
    <section className="chat-page">
      <PageHeader
        title="Чат с buddy"
        hint="Ответы основаны на тестовых инструкциях прототипа."
        actions={
          <>
            <Button variant="secondary" size="sm" className="chat-requests-btn" onClick={() => setDrawer(true)}>
              Мои заявки ({actions.data?.length ?? 0})
            </Button>
            <Button variant="secondary" size="sm" onClick={newDialog}>
              Новый диалог
            </Button>
          </>
        }
      />
      <div className="chat-body">
        <div className="chat-main">
          <div className="chat-list" aria-live="polite">
            {state.messages.length === 0 && !restoring && <WelcomeBubble name={firstName} />}
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
          <ChatInput disabled={state.streaming || restoring} onSend={send} />
        </div>
        <aside className="chat-aside" aria-label="Мои заявки">
          <h2>Мои заявки</h2>
          <ActionsPanel query={actions} />
        </aside>
      </div>
      {drawer && (
        <RequestsDrawer onClose={closeDrawer}>
          <ActionsPanel query={actions} />
        </RequestsDrawer>
      )}
    </section>
  )
}
