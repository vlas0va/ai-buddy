import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { Logo } from '@/components/ui/Logo'
import { handlers } from '@/mocks/handlers'
import { signInAs } from '@/test/mockWorld'
import { BenefitPanel } from './BenefitPanel'

const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const mount = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <BenefitPanel />
    </QueryClientProvider>,
  )

const benefit = {
  interns: 2,
  minutes_per_question: 10,
  asked: 20,
  escalated: 5,
  answered: 4,
  added_to_kb: 3,
  self_served: 15,
  self_served_percent: 75,
  saved_minutes: 150,
  useful: 7,
  not_useful: 1,
  rows: [
    { user_id: 'intern.anna', display_name: 'Анна Котова', asked: 12, escalated: 2, self_served: 10, saved_minutes: 100, useful: 5, not_useful: 1 },
    { user_id: 'intern.boris', display_name: 'Борис Лебедев', asked: 8, escalated: 3, self_served: 5, saved_minutes: 50, useful: 2, not_useful: 0 },
  ],
}

describe('Idea 13.11: «Польза buddy» for the mentor and the lead', () => {
  it('shows the share closed by buddy, the saved time, marks and a line per intern', async () => {
    signInAs('mentor.oleg')
    server.use(http.get('*/api/team/benefit', () => HttpResponse.json(benefit)))
    mount()
    expect(await screen.findByText('75%')).toBeInTheDocument()
    expect(screen.getByText('вопросов стажёров buddy закрыл сам: 15 из 20')).toBeInTheDocument()
    expect(screen.getByText('≈ 2 ч 30 мин')).toBeInTheDocument()
    expect(screen.getByRole('meter', { name: 'Доля вопросов, закрытых buddy' })).toHaveAttribute('aria-valuenow', '75')
    expect(screen.getByText('«Полезно» из 8 оценок')).toBeInTheDocument()
    expect(screen.getByText('вопросов передано наставнику')).toBeInTheDocument()
    const row = screen.getByRole('row', { name: /Анна Котова/ })
    expect(within(row).getByText('1 ч 40 мин')).toBeInTheDocument()
  })

  it('without questions it explains what will appear', async () => {
    signInAs('lead.maria')
    server.use(http.get('*/api/team/benefit', () => HttpResponse.json({ ...benefit, asked: 0, self_served: 0, self_served_percent: 0, rows: [] })))
    mount()
    expect(await screen.findByText(/ещё не задавали вопросов buddy/)).toBeInTheDocument()
  })

  it('the dev mock answers the mentor with the interns of the team', async () => {
    signInAs('mentor.oleg')
    mount()
    expect(await screen.findByText(/вопросов стажёров buddy закрыл сам/)).toBeInTheDocument()
  })
})

describe('Idea 13.11: the buddy pair mark', () => {
  it('large sizes draw faces and the accessory, small sizes stay simple', () => {
    const { container, rerender } = render(<Logo size={40} />)
    const big = container.querySelectorAll('path').length
    rerender(<Logo size={28} />)
    expect(container.querySelectorAll('path').length).toBeLessThan(big)
    rerender(<Logo size={40} variant="E7" />)
    expect(container.querySelectorAll('rect').length).toBe(3) // background + headset ear pads
    rerender(<Logo size={40} variant="nope" />)
    expect(container.querySelector('svg')).toBeInTheDocument()
  })
})
