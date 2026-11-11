import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Proposal } from '@/chat/events'
import { withoutApprover } from '@/chat/statusText'
import { jsonResponse } from '@/test/helpers'
import { ActionProposalCard } from './ActionProposalCard'
import { ActionResultCard } from './ActionResultCard'
import { ChatInput } from './ChatInput'
import { ErrorBlock } from './ErrorBlock'
import { EscalationDraft } from './EscalationDraft'
import { MessageBubble, WelcomeBubble } from './MessageBubble'
import { RefusalBlock } from './RefusalBlock'
import { SourceChips } from './SourceChips'

const absence: Proposal & { state: 'pending' } = {
  proposal_id: 'p1',
  kind: 'absence_request',
  state: 'pending',
  draft: {
    tool: 'absence.create_request',
    args: { type: 'day_off', start_date: '2026-11-13', end_date: '2026-11-13' },
    display: { approver_display_name: 'Мария Лебедева', dates: '13.11.2026' },
  },
}

describe('ActionProposalCard (S3, R66)', () => {
  it('shows dates, type and approver and sends nothing until the button is clicked', async () => {
    const onConfirm = vi.fn()
    render(<ActionProposalCard proposal={absence} onConfirm={onConfirm} onCancel={vi.fn()} />)
    expect(screen.getByText('13.11.2026')).toBeInTheDocument()
    expect(screen.getByText('Отгул')).toBeInTheDocument()
    expect(screen.getByText('Мария Лебедева')).toBeInTheDocument()
    expect(onConfirm).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Отправить' }))
    expect(onConfirm).toHaveBeenCalledWith('p1')
  })

  it('shows a loading button that cannot be clicked twice while confirming (R79)', async () => {
    const onConfirm = vi.fn()
    render(<ActionProposalCard proposal={{ ...absence, state: 'confirming' } as never} onConfirm={onConfirm} onCancel={vi.fn()} />)
    const btn = screen.getByRole('button', { name: 'Отправить' })
    expect(btn).toBeDisabled()
    await userEvent.click(btn)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('cancel calls onCancel; finished states hide the buttons', async () => {
    const onCancel = vi.fn()
    const { rerender } = render(<ActionProposalCard proposal={absence} onConfirm={vi.fn()} onCancel={onCancel} />)
    await userEvent.click(screen.getByRole('button', { name: 'Отменить' }))
    expect(onCancel).toHaveBeenCalledWith('p1')
    rerender(<ActionProposalCard proposal={{ ...absence, state: 'cancelled' } as never} onConfirm={vi.fn()} onCancel={onCancel} />)
    expect(screen.queryByRole('button', { name: 'Отправить' })).not.toBeInTheDocument()
    expect(screen.getByText(/отменена/)).toBeInTheDocument()
    rerender(<ActionProposalCard proposal={{ ...absence, state: 'confirmed' } as never} onConfirm={vi.fn()} onCancel={onCancel} />)
    expect(screen.getByText('Заявка отправлена.')).toBeInTheDocument()
  })

  it('renders an access request draft and falls back to start-end dates', () => {
    const access: Proposal & { state: 'pending' } = {
      proposal_id: 'p2',
      kind: 'access_request',
      state: 'pending',
      draft: { tool: 'access.request_access', args: { resource_id: 'space-delivery' }, display: { elevated: true } },
    }
    const { rerender } = render(<ActionProposalCard proposal={access} onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('space-delivery')).toBeInTheDocument()
    expect(screen.getByText('Да, нужно согласование')).toBeInTheDocument()
    rerender(
      <ActionProposalCard
        proposal={{ ...absence, draft: { ...absence.draft, args: { type: 'vacation', start_date: '2026-11-13', end_date: '2026-11-15' }, display: {} } }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('2026-11-13 — 2026-11-15')).toBeInTheDocument()
  })
})

function withClient(ui: ReactElement) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse([])))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{ui}</QueryClientProvider>
}

describe('ActionResultCard (S3, R66)', () => {
  it('shows number, status, approver and the saved note', () => {
    render(
      withClient(
        <ActionResultCard
          result={{ action_id: 'a1', kind: 'absence_request', external_id: 'ABS-1042', status: 'pending_approval', approver_display_name: 'Мария Лебедева', summary: 'Отгул 13.11.2026' }}
        />,
      ),
    )
    expect(screen.getByText(/ABS-1042/)).toBeInTheDocument()
    expect(screen.getByText('На согласовании')).toBeInTheDocument()
    expect(screen.getByText('Согласующий: Мария Лебедева')).toBeInTheDocument()
    expect(screen.getByText('Сохранено в журнале действий')).toBeInTheDocument()
  })

  it('falls back to the action id', () => {
    render(withClient(<ActionResultCard result={{ action_id: 'a9', kind: 'access_request', status: 'granted' }} />))
    expect(screen.getByText(/a9/)).toBeInTheDocument()
    expect(screen.getByText('Доступ выдан')).toBeInTheDocument()
  })
})

describe('SourceChips (R69)', () => {
  it('shows chips with the test badge', () => {
    render(<SourceChips sources={[{ doc_id: 'd1', title: 'Порядок отгула', source_type: 'test' }]} />)
    expect(screen.getByText('Порядок отгула')).toBeInTheDocument()
    expect(screen.getByText('Тестовая инструкция прототипа')).toBeInTheDocument()
  })
  it('renders nothing without sources', () => {
    const { container } = render(<SourceChips sources={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('withoutApprover', () => {
  it('drops the approver suffix of the summary', () => {
    expect(withoutApprover('Отгул 13.11.2026, согласующий — Мария Лебедева')).toBe('Отгул 13.11.2026')
    expect(withoutApprover('Доступ к Jira')).toBe('Доступ к Jira')
  })
})

describe('ErrorBlock (R79)', () => {
  it('offers retry only for retryable errors', async () => {
    const onRetry = vi.fn()
    const { rerender } = render(<ErrorBlock error={{ code: 'x', message: 'Сбой сервиса', retryable: true }} onRetry={onRetry} />)
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(onRetry).toHaveBeenCalled()
    rerender(<ErrorBlock error={{ code: 'x', message: 'Сбой сервиса', retryable: false }} onRetry={onRetry} />)
    expect(screen.queryByRole('button', { name: 'Повторить' })).not.toBeInTheDocument()
  })
})

describe('RefusalBlock (R71, R72)', () => {
  it('redirects to the right role', () => {
    render(<RefusalBlock refusal={{ reason: 'out_of_scope', redirect_role: 'hrbp' }} />)
    expect(screen.getByRole('note')).toHaveTextContent('Обратитесь к HR BP.')
  })
  it('explains missing rights', () => {
    render(<RefusalBlock refusal={{ reason: 'no_rights', redirect_role: null }} />)
    expect(screen.getByRole('note')).toHaveTextContent('нет доступа')
  })
})

describe('ChatInput', () => {
  it('Enter sends, Shift+Enter adds a newline, empty text is ignored', async () => {
    const onSend = vi.fn()
    render(<ChatInput disabled={false} onSend={onSend} />)
    const box = screen.getByLabelText('Сообщение')
    await userEvent.type(box, '{Enter}')
    expect(onSend).not.toHaveBeenCalled()
    await userEvent.type(box, 'a{Shift>}{Enter}{/Shift}b')
    expect(onSend).not.toHaveBeenCalled()
    expect(box).toHaveValue('a\nb')
    await userEvent.type(box, '{Enter}')
    expect(onSend).toHaveBeenCalledWith('a\nb', [])
    expect(box).toHaveValue('')
  })

  it('does not send while streaming: the send button is disabled and Enter is ignored', async () => {
    const onSend = vi.fn()
    render(<ChatInput disabled onSend={onSend} />)
    await userEvent.type(screen.getByLabelText('Сообщение'), 'привет{Enter}')
    expect(screen.getByRole('button', { name: 'Отправить' })).toBeDisabled()
    expect(onSend).not.toHaveBeenCalled()
  })

  it('send button submits', async () => {
    const onSend = vi.fn()
    render(<ChatInput disabled={false} onSend={onSend} />)
    await userEvent.type(screen.getByLabelText('Сообщение'), 'привет')
    await userEvent.click(screen.getByRole('button', { name: 'Отправить' }))
    expect(onSend).toHaveBeenCalledWith('привет', [])
  })
})

describe('ChatInput suggestions (combobox)', () => {
  const setup = () => {
    const onSend = vi.fn()
    render(<ChatInput disabled={false} onSend={onSend} />)
    return { onSend, box: screen.getByRole('combobox', { name: 'Сообщение' }) }
  }

  it('opens on focus with 6 grouped options and closes on Escape', async () => {
    const { box } = setup()
    expect(box).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(box)
    expect(box).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    expect(screen.getAllByRole('option')).toHaveLength(6)
    expect(screen.getByRole('group', { name: 'Доступы' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('filters by typed text, case-insensitive', async () => {
    const { box } = setup()
    await userEvent.type(box, 'ОТГУЛ')
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['Хочу взять отгул в пятницу', 'Как оформить отгул?'])
    await userEvent.type(box, 'zzz')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('ArrowDown/ArrowUp move the active option and Enter sends it', async () => {
    const { box, onSend } = setup()
    await userEvent.click(box)
    await userEvent.keyboard('{ArrowDown}{ArrowDown}')
    const options = screen.getAllByRole('option')
    expect(options[1]).toHaveAttribute('aria-selected', 'true')
    expect(box).toHaveAttribute('aria-activedescendant', options[1]!.id)
    await userEvent.keyboard('{ArrowUp}')
    expect(box).toHaveAttribute('aria-activedescendant', options[0]!.id)
    await userEvent.keyboard('{ArrowUp}')
    expect(box).toHaveAttribute('aria-activedescendant', options[5]!.id) // wraps
    await userEvent.keyboard('{Enter}')
    expect(onSend).toHaveBeenCalledWith('Что мне сделать на этой неделе?', [])
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('click picks and sends; outside click closes; the toggle button toggles', async () => {
    const { box, onSend } = setup()
    await userEvent.click(box)
    await userEvent.click(screen.getByRole('option', { name: 'Кто мой наставник?' }))
    expect(onSend).toHaveBeenCalledWith('Кто мой наставник?', [])
    await userEvent.click(screen.getByRole('button', { name: 'Подсказки' }))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    await userEvent.click(document.body)
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Подсказки' }))
    await userEvent.click(screen.getByRole('button', { name: 'Подсказки' }))
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })
})

describe('MessageBubble', () => {
  it('welcome bubble greets by name', () => {
    render(<WelcomeBubble name="Анна" />)
    expect(screen.getByText(/Привет, Анна! Я помогу с адаптацией/)).toBeInTheDocument()
  })

  it('renders user and buddy bubbles, typing indicator and status', () => {
    const base = { id: '1', text: 'Привет', sources: [], proposal: null, result: null, escalation: null, refusal: null, error: null }
    const { rerender } = render(<MessageBubble message={{ ...base, author: 'user' }} typing={false} status={null} />)
    expect(screen.getByText('Привет')).toBeInTheDocument()
    rerender(<MessageBubble message={{ ...base, author: 'assistant', text: '' }} typing status="Ищу в базе знаний…" />)
    expect(screen.getByLabelText('Buddy печатает')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Ищу в базе знаний…')
  })
})

describe('MessageBubble layout rules', () => {
  const base = { id: '1', text: '', sources: [], proposal: null, result: null, escalation: null, refusal: null, error: null, author: 'assistant' as const }

  it('an assistant message without text shows the error next to the avatar, no empty bubble', () => {
    const { container } = render(
      <MessageBubble message={base} typing={false} status={null} error={<ErrorBlock error={{ code: 'internal', message: 'Сбой', retryable: false }} />} />,
    )
    expect(container.querySelector('.chat-bubble')).toBeNull()
    expect(container.querySelector('.chat-row-buddy .chat-col [role="alert"]')).not.toBeNull()
  })

  it('status line comes before the text and disappears once tokens arrive or the answer is finished', () => {
    const { container, rerender } = render(<MessageBubble message={base} typing status="Ищу в базе знаний…" />)
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(container.querySelector('.chat-status')!.compareDocumentPosition(container.querySelector('.chat-bubble')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    rerender(<MessageBubble message={{ ...base, text: 'Ответ' }} typing status="Ищу в базе знаний…" />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    rerender(<MessageBubble message={{ ...base, text: 'Ответ' }} typing={false} status="Сохраняю…" />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})

describe('EscalationDraft', () => {
  it('edits the text and posts it to /api/escalations', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ id: 'e1', to_role: 'mentor', text: 'Новый текст', status: 'open', created_at: '2026-11-09T10:00:00Z' }, 201),
    )
    vi.stubGlobal('fetch', fetchMock)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <EscalationDraft draft={{ to_role: 'mentor', text: 'Здравствуйте' }} />
      </QueryClientProvider>,
    )
    const box = screen.getByLabelText('Текст сообщения')
    await userEvent.clear(box)
    await userEvent.type(box, 'Новый текст')
    await userEvent.click(screen.getByRole('button', { name: 'Отправить наставнику' }))
    expect(await screen.findByText('Сообщение отправлено.')).toBeInTheDocument()
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/escalations')
    expect(JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body))).toMatchObject({ to_role: 'mentor', text: 'Новый текст' })
  })

  it('shows an error when sending fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'internal', message: 'Сбой отправки', retryable: true }, 500)))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <EscalationDraft draft={{ to_role: 'lead', text: 'Текст' }} />
      </QueryClientProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Отправить наставнику' }))
    await waitFor(() => expect(screen.getByText('Сбой отправки')).toBeInTheDocument())
  })
})
