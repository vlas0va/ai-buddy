import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import type { Goal } from '@/api/schemas'
import { handlers } from '@/mocks/handlers'
import { signInAs } from '@/test/mockWorld'
import { GoalsSection } from './GoalsSection'

const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const wrap = (goals: Goal[], editable: boolean, canPropose = false) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <GoalsSection internId="intern.anna" goals={goals} editable={editable} canPropose={canPropose} planKey={['plan', 'x']} />
    </QueryClientProvider>,
  )
}

describe('Organizers 12.11: goals are agreed with the lead, approved and tracked', () => {
  it('the lead approves a proposed goal and marks an active one achieved', async () => {
    signInAs('lead.maria')
    const sent: unknown[] = []
    server.use(
      http.put('*/api/plan/goals', async ({ request }) => {
        const body = (await request.json()) as { goals: unknown[] }
        sent.push(body.goals)
        return HttpResponse.json([])
      }),
    )
    wrap(
      [
        { id: 'g1', created_by: 'intern.anna', title: 'Выкатить задачу в прод', criteria: 'PR влит', status: 'proposed' },
        { id: 'g2', created_by: 'mentor.oleg', title: 'Разобраться в CI', status: 'active' },
      ],
      true,
    )
    expect(screen.getByText('Ждёт согласования')).toBeInTheDocument()
    expect(screen.getByText(/достигнуто 0 из 1/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Согласовать' }))
    expect(sent.at(-1)).toEqual([
      { title: 'Выкатить задачу в прод', criteria: 'PR влит', status: 'active' },
      { title: 'Разобраться в CI', status: 'active' },
    ])
    await userEvent.click(screen.getAllByRole('button', { name: 'Достигнута' })[0])
    expect((sent.at(-1) as { status: string }[])[1].status).toBe('achieved')
  })

  it('the intern writes down the goals after the meeting; they go for approval', async () => {
    signInAs('intern.anna')
    let body: unknown = null
    server.use(
      http.post('*/api/plan/goals/propose', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json([], { status: 201 })
      }),
    )
    wrap([], false, true)
    expect(screen.getByText(/Цели обсуждают с руководителем на первой неделе/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Согласовать' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Записать цели после встречи' }))
    await userEvent.type(screen.getByLabelText('Цель 1'), 'Выкатить первую задачу')
    await userEvent.type(screen.getByLabelText('Критерий 1'), 'PR влит')
    await userEvent.click(screen.getByRole('button', { name: 'Отправить на согласование' }))
    expect(body).toEqual({ goals: [{ title: 'Выкатить первую задачу', criteria: 'PR влит' }] })
  })
})
