import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { attachmentKind, formatSize, readAttachment, safeUrl } from '@/chat/attachments'
import type { Proposal } from '@/chat/events'
import { absenceSteps } from '@/chat/statusText'
import { fromHistory, initialState, restartTurn, startTurn } from '@/chat/stream'
import { ActionProposalCard } from './ActionProposalCard'
import { ActionResultCard } from './ActionResultCard'
import { ActionsPanel } from './ActionsPanel'
import { ChatInput } from './ChatInput'
import { MessageBubble } from './MessageBubble'
import { SourceChips } from './SourceChips'

// Trips, consents, HR steps of a day off, attachments and external sources (openspec trips-consents, attachments-websearch).

function withClient(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{ui}</QueryClientProvider>
}

const proposal = (kind: Proposal['kind'], args: Record<string, unknown>, display: Record<string, unknown>) =>
  ({ proposal_id: 'p', kind, state: 'pending', draft: { tool: 't', args, display } }) as Proposal & { state: 'pending' }

describe('proposal cards for trips, consents and decisions', () => {
  it('TP-02 trip draft shows city, dates, purpose and approver', () => {
    render(
      <ActionProposalCard
        proposal={proposal(
          'trip_request',
          { city: 'Казань', start_date: '2026-11-23', end_date: '2026-11-25', purpose: 'встреча' },
          { approver_display_name: 'Мария Лебедева' },
        )}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('Черновик заявки на командировку')).toBeInTheDocument()
    expect(screen.getByText('Казань')).toBeInTheDocument()
    expect(screen.getByText('2026-11-23 — 2026-11-25')).toBeInTheDocument()
    expect(screen.getByText('встреча')).toBeInTheDocument()
  })

  it('CN-02 consent and lead decision drafts', () => {
    const { unmount } = render(
      <ActionProposalCard
        proposal={proposal('consent', { consent_id: 'photo' }, { consent_title: 'Фото и видео', description: 'рассылки' })}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('Подпись согласия')).toBeInTheDocument()
    expect(screen.getByText('Фото и видео')).toBeInTheDocument()
    expect(screen.getByText('через HR BP')).toBeInTheDocument()
    unmount()
    render(
      <ActionProposalCard
        proposal={proposal(
          'approval_decision',
          { approval_id: 'APR-1', decision: 'approved' },
          { ref_id: 'TRP-000200', requester_display_name: 'Анна', request_summary: 'Командировка', decision_label: 'Согласовать' },
        )}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('Решение по заявке')).toBeInTheDocument()
    expect(screen.getByText('TRP-000200 — Анна')).toBeInTheDocument()
    render(
      <ActionProposalCard proposal={proposal('trip_request', { city: 'Казань', start_date: '2026-11-23' }, {})} onConfirm={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(screen.getAllByText('2026-11-23').length).toBeGreaterThan(0)
  })
})

describe('day off draft tiles', () => {
  it('shows working days when the agent sends them', () => {
    render(
      <ActionProposalCard
        proposal={proposal('absence_request', { type: 'day_off', start_date: '2026-11-18', end_date: '2026-11-18' }, { working_days: 1 })}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('Рабочих дней')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })
})

describe('HR steps of a day off (Q&A 10.11)', () => {
  it('chain follows the status', () => {
    expect(absenceSteps('approved').map((s) => s.state)).toEqual(['done', 'done', 'done'])
    expect(absenceSteps('rejected').map((s) => s.state)).toEqual(['done', 'skipped', 'skipped'])
    expect(absenceSteps('pending_approval').map((s) => s.state)).toEqual(['current', 'waiting', 'waiting'])
  })

  it('result card and requests panel show the steps', () => {
    render(withClient(<ActionResultCard result={{ action_id: 'a1', kind: 'absence_request', status: 'approved', external_id: 'ABS-1' }} />))
    expect(screen.getByLabelText('Шаги согласования')).toHaveTextContent('HR-администратор: оформил в кадровой системе')
    render(
      <ActionsPanel
        query={{
          data: [
            { id: 'a2', kind: 'absence_request', status: 'approved', summary: 'Отгул', created_at: '' },
            { id: 'a3', kind: 'consent', status: 'signed', summary: 'Согласие: фото — подписано', created_at: '' },
          ],
          isPending: false,
          error: null,
          refetch: vi.fn(),
        }}
      />,
    )
    expect(screen.getByText('Подписано')).toBeInTheDocument()
    const lists = screen.getAllByLabelText('Шаги согласования')
    expect(lists).toHaveLength(2)
    for (const l of lists) expect(l).toHaveTextContent('HR BP: уведомлён')
  })
})

describe('external sources (WS-01)', () => {
  it('external chips are links with their own badge, never the KB badge', () => {
    render(
      <SourceChips
        sources={[
          { doc_id: 'web:1', title: 'Pro Git', source_type: 'external', url: 'https://git-scm.com/book' },
          { doc_id: 'web:2', title: 'Bad', source_type: 'external', url: 'javascript:alert(1)' },
        ]}
      />,
    )
    expect(screen.getByRole('link', { name: 'Pro Git' })).toHaveAttribute('href', 'https://git-scm.com/book')
    expect(screen.getByText('Bad').getAttribute('href')).toBeNull()
    expect(screen.getByText('Внешний источник')).toBeInTheDocument()
    expect(screen.queryByText('Тестовая инструкция прототипа')).not.toBeInTheDocument()
    expect(safeUrl(undefined)).toBeUndefined()
  })
})

const file = (name: string, type: string, body = 'x') => new File([body], name, { type })

describe('attachments (AT-xx)', () => {
  it('kind by type and extension, size labels', async () => {
    expect(attachmentKind(file('a.png', 'image/png'))).toBe('image')
    expect(attachmentKind(file('a.pdf', ''))).toBe('document')
    expect(attachmentKind(file('app.log', ''))).toBe('text')
    expect(attachmentKind(file('.env', ''))).toBe('text')
    expect(attachmentKind(file('a.exe', 'application/octet-stream'))).toBeNull()
    expect(formatSize(10)).toBe('10 Б')
    expect(formatSize(2048)).toBe('2 КБ')
    expect(formatSize(3 * 1024 * 1024)).toBe('3.0 МБ')
    expect(await readAttachment(file('a.log', 'text/plain', 'ERROR x'))).toMatchObject({ kind: 'text', text: 'ERROR x' })
    expect(await readAttachment(file('s.png', 'image/png'))).not.toHaveProperty('text')
    expect(await readAttachment(file('a.exe', ''))).toMatch(/не поддерживается/)
    expect(await readAttachment(file('big.log', 'text/plain', 'a'.repeat(33 * 1024)))).toMatch(/32 КБ/)
    const huge = file('v.png', 'image/png')
    Object.defineProperty(huge, 'size', { value: 21 * 1024 * 1024 })
    expect(await readAttachment(huge)).toMatch(/20 МБ/)
  })

  it('ChatInput attaches, removes, limits to 3 and sends files without text', async () => {
    const onSend = vi.fn()
    render(<ChatInput disabled={false} onSend={onSend} />)
    const input = screen.getByTestId('chat-file-input') as HTMLInputElement
    await userEvent.upload(input, [file('app.log', 'text/plain', 'ERROR boom'), file('s.png', 'image/png')])
    expect(await screen.findByText(/app\.log · файл/)).toBeInTheDocument()
    expect(screen.getByText(/s\.png · скриншот/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать s.png' }))
    expect(screen.queryByText(/s\.png/)).not.toBeInTheDocument()
    await userEvent.upload(input, [file('a.txt', 'text/plain'), file('b.txt', 'text/plain'), file('c.txt', 'text/plain')])
    expect(await screen.findByRole('alert')).toHaveTextContent('не больше 3 файлов')
    await userEvent.click(screen.getByRole('button', { name: 'Отправить' }))
    expect(onSend).toHaveBeenCalledWith('', expect.arrayContaining([expect.objectContaining({ name: 'app.log', text: 'ERROR boom' })]))
    expect(onSend.mock.calls[0]?.[1]).toHaveLength(3)
    expect(screen.queryByText(/app\.log/)).not.toBeInTheDocument()
  })

  it('user bubble lists attachments; history and retry keep them', () => {
    const s = startTurn(initialState, '', [{ name: 'app.log', kind: 'text', size: 10, text: 'secret' }])
    expect(s.messages[0]?.attachments).toEqual([{ name: 'app.log', kind: 'text', size: 10, mime: undefined }])
    expect(s.messages[1]?.retryAttachments).toHaveLength(1)
    const r = restartTurn(s, s.messages[1]!.id)
    expect(r.messages[1]?.retryAttachments).toHaveLength(1)
    render(<MessageBubble message={s.messages[0]!} typing={false} status={null} />)
    expect(screen.getByLabelText('Вложения')).toHaveTextContent('app.log · файл, 10 Б')
    const h = fromHistory({
      session_id: 's',
      messages: [{ id: '1', author: 'user', text: '', attachments: [{ name: 'a.pdf', kind: 'document', size: 5 }], created_at: '' }],
    })
    expect(h.messages[0]?.attachments?.[0]?.kind).toBe('document')
  })
})

describe('suggestions by role (checkpoint 11.11)', () => {
  it('lead and mentor get their own list; unknown roles fall back to the intern list', async () => {
    const { filterSuggestions } = await import('@/chat/suggestions')
    const items = (role?: string) => filterSuggestions('', role).flatMap((g) => g.items)
    expect(items('lead')).toContain('Покажи заявки на согласование')
    expect(items('mentor')).toContain('Подготовь меня к 1:1 со стажёром')
    expect(items('mentor')).not.toContain('Хочу взять отгул в пятницу')
    expect(items('robot')).toEqual(items('intern'))
    expect(filterSuggestions('1:1', 'lead')[0]?.items).toEqual(['Подготовь меня к 1:1 со стажёром'])
  })
})

describe('Checkpoint 11.11: absence types, HR admin stage, reschedule', () => {
  it('absence steps: after the lead, the HR admin is the current step', () => {
    expect(absenceSteps('pending_hr').map((s) => s.state)).toEqual(['done', 'current', 'waiting'])
  })

  it('vacation draft shows the type, calendar days, documents and both approvers', () => {
    const p = {
      proposal_id: 'v1',
      kind: 'absence_request',
      state: 'pending',
      draft: {
        tool: 'absence.create_request',
        args: { type: 'vacation', start_date: '2026-12-01', end_date: '2026-12-10' },
        display: { dates: '01.12.2026–10.12.2026', calendar_days: 10, documents: ['заявление.pdf'], approver_display_name: 'Мария Лебедева' },
      },
    } as unknown as Proposal & { state: 'pending' }
    render(<ActionProposalCard proposal={p} onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('Отпуск')).toBeInTheDocument()
    expect(screen.getByText('Календарных дней')).toBeInTheDocument()
    expect(screen.getByText('заявление.pdf')).toBeInTheDocument()
    expect(screen.getByText('HR-администратор')).toBeInTheDocument()
  })

  it('reschedule draft shows the old and the new dates', () => {
    const p = {
      proposal_id: 'r1',
      kind: 'absence_reschedule',
      state: 'pending',
      draft: {
        tool: 'absence.reschedule_request',
        args: { id: 'ABS-000101', start_date: '2026-12-08', end_date: '2026-12-17' },
        display: { ref_id: 'ABS-000101', type_label: 'Отпуск', old_period_label: 'с 1 по 10 декабря', period_label: 'с 8 по 17 декабря' },
      },
    } as unknown as Proposal & { state: 'pending' }
    render(<ActionProposalCard proposal={p} onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('Перенос заявки')).toBeInTheDocument()
    expect(screen.getByText('ABS-000101 — Отпуск')).toBeInTheDocument()
    expect(screen.getByText('с 8 по 17 декабря')).toBeInTheDocument()
  })
})
