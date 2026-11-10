import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { resetMockState, state } from '@/mocks/db'
import { mockConfig } from '@/mocks/sse'
import { mountWithClient, seedRequest, signInAs } from '@/test/mockWorld'
import { ApprovalsPage } from './ApprovalsPage'

const server = setupServer(...handlers)
const decisionCalls: { id: string; key: string | null; body: unknown }[] = []

beforeAll(() => server.listen())
afterAll(() => server.close())
afterEach(() => server.resetHandlers())

beforeEach(() => {
  resetMockState()
  mockConfig.stepDelayMs = 0
  decisionCalls.length = 0
  server.events.removeAllListeners()
  server.events.on('request:start', async ({ request }) => {
    if (request.method === 'POST' && request.url.includes('/decision')) {
      decisionCalls.push({ id: request.url.split('/').at(-2)!, key: request.headers.get('Idempotency-Key'), body: await request.clone().json() })
    }
  })
})

describe('S3 step 8: manager approvals page', () => {
  it('S3_R66: lead sees who/what/when and approves with a comment only after a click', async () => {
    const action = seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    mountWithClient(<ApprovalsPage />)

    const card = await screen.findByLabelText('Заявка: Анна Соколова')
    expect(within(card).getByText('Отсутствие')).toBeInTheDocument()
    expect(within(card).getByText(/^Отгул \d{2}\.\d{2}\.\d{4}$/)).toBeInTheDocument()
    expect(decisionCalls).toHaveLength(0) // nothing is decided without a click

    await userEvent.type(within(card).getByLabelText('Комментарий для Анна Соколова'), 'Хорошего отдыха')
    await userEvent.click(within(card).getByRole('button', { name: 'Согласовать' }))

    expect(await screen.findByText('Заявок на согласование нет.')).toBeInTheDocument()
    expect(decisionCalls[0]).toMatchObject({ id: action.id, body: { decision: 'approved', comment: 'Хорошего отдыха' } })
    expect(state.actions.get('intern.anna')![0]!.status).toBe('approved')
    expect(state.extraTasks.get('intern.anna')![0]!.status).toBe('done')
  })

  it('S3_R66: reject updates the request to "rejected"', async () => {
    seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    mountWithClient(<ApprovalsPage />)
    await userEvent.click(await screen.findByRole('button', { name: 'Отклонить' }))
    await screen.findByText('Заявок на согласование нет.')
    expect(state.actions.get('intern.anna')![0]!.status).toBe('rejected')
    expect(decisionCalls[0]!.body).toEqual({ decision: 'rejected' })
  })

  it('S3_R79: a double click sends exactly one decision', async () => {
    seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    mountWithClient(<ApprovalsPage />)
    await userEvent.dblClick(await screen.findByRole('button', { name: 'Согласовать' }))
    await screen.findByText('Заявок на согласование нет.')
    expect(decisionCalls).toHaveLength(1)
    expect(decisionCalls[0]!.key).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('S3_R79: buttons are disabled while the request is in flight', async () => {
    seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    server.use(
      http.post('*/api/approvals/:id/decision', async () => {
        await gate
        return HttpResponse.json({ code: 'internal', message: 'Сбой сервера.', retryable: true }, { status: 500 })
      }),
    )
    mountWithClient(<ApprovalsPage />)
    await userEvent.click(await screen.findByRole('button', { name: 'Согласовать' }))
    expect(screen.getByRole('button', { name: /Согласовать/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Отклонить' })).toBeDisabled()
    release()
    expect(await screen.findByText('Сбой сервера.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Согласовать' })).toBeEnabled()
  })

  it('S3_R79: a retry after an error reuses the same Idempotency-Key of the card', async () => {
    seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    server.use(
      http.post('*/api/approvals/:id/decision', () => HttpResponse.json({ code: 'upstream_unavailable', message: 'Сервис недоступен.', retryable: true }, { status: 503 }), { once: true }),
    )
    mountWithClient(<ApprovalsPage />)
    await userEvent.click(await screen.findByRole('button', { name: 'Согласовать' }))
    expect(await screen.findByText('Сервис недоступен.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Согласовать' }))
    await screen.findByText('Заявок на согласование нет.')
    const keys = decisionCalls.map((c) => c.key)
    expect(keys).toHaveLength(2)
    expect(keys[0]).toBe(keys[1])
  })

  it('S3: a decision on an already decided request is a 409 shown to the lead', async () => {
    const action = seedRequest('intern.anna', 'хочу отгул в пятницу')
    signInAs('lead.maria')
    mountWithClient(<ApprovalsPage />)
    const approve = await screen.findByRole('button', { name: 'Согласовать' })
    action.status = 'rejected' // decided elsewhere while the page was open
    await userEvent.click(approve)
    expect(await screen.findByText('По этой заявке уже принято решение.')).toBeInTheDocument()
  })

  it('S3: empty state and the server-side 403 for an intern', async () => {
    signInAs('lead.maria')
    const lead = mountWithClient(<ApprovalsPage />)
    expect(await screen.findByText('Заявок на согласование нет.')).toBeInTheDocument()
    lead.unmount()

    signInAs('intern.anna')
    mountWithClient(<ApprovalsPage />)
    expect(await screen.findByText('Недостаточно прав для этого действия.')).toBeInTheDocument()
    await waitFor(() => expect(decisionCalls).toHaveLength(0))
  })
})
