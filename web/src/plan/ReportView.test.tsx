import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { signInAs } from '@/test/mockWorld'
import { ReportView } from './ReportView'

const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const report = {
  user_id: 'intern.anna',
  name: 'Анна Котова',
  cohort_start: '2026-11-02',
  days: 10,
  total_days: 90,
  final: false,
  current_period: 'week_2',
  tasks: { total: 16, done: 2, early: 1, on_time: 0, late: 1, overdue_now: 3, upcoming: 10 },
  periods: [{ period: 'week_1', total: 5, done: 2, late: 1 }],
  late: [{ title: 'Вступи в каналы', due_at: '2026-11-09', completed_at: '2026-11-12' }],
  overdue: [{ title: 'Познакомься с командой', due_at: '2026-11-09', completed_at: null }],
  questions: { asked: 12, escalated: 2, answered: 1, added_to_kb: 1 },
  accesses_requested: 3,
  goals: 0,
  discuss: ['Просрочена задача «Познакомься с командой» — что мешает и чем помочь.'],
}

const wrap = (userId?: string) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ReportView userId={userId} />
    </QueryClientProvider>,
  )
}

describe('Idea 12.11: internship report', () => {
  it('the mentor sees facts: tasks by timeliness, periods, questions and topics for the 1:1', async () => {
    signInAs('mentor.oleg')
    let asked = ''
    server.use(
      http.get('*/api/plan/report', ({ request }) => {
        asked = new URL(request.url).searchParams.get('user_id') ?? ''
        return HttpResponse.json(report)
      }),
    )
    wrap('intern.anna')
    const tasks = await screen.findByRole('generic', { name: 'Задачи плана' })
    expect(asked).toBe('intern.anna')
    expect(within(tasks).getByText('Задачи плана: выполнено 2 из 16')).toBeInTheDocument()
    expect(within(tasks).getByText('раньше срока').previousSibling).toHaveTextContent('1')
    expect(within(tasks).getByText(/Вступи в каналы — срок 09\.11/)).toBeInTheDocument()
    expect(screen.getByText(/Промежуточный отчёт: день 10 из 90/)).toHaveTextContent('Этот же отчёт видит стажёр.')
    expect(screen.getByText('ответов добавлено в базу знаний').previousSibling).toHaveTextContent('1')
    expect(screen.getByText(/что мешает и чем помочь/)).toBeInTheDocument()
  })

  it('the intern sees the same report, marked final after three months', async () => {
    signInAs('intern.anna')
    server.use(http.get('*/api/plan/report', () => HttpResponse.json({ ...report, final: true, days: 95 })))
    wrap()
    expect(await screen.findByText(/Итоговый отчёт: день 90 из 90/)).toHaveTextContent('Этот же отчёт видит твой наставник.')
  })
})
