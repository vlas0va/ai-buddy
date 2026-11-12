import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { setupServer } from 'msw/node'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { signInAs } from '@/test/mockWorld'
import { SourceChips } from './SourceChips'

const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterAll(() => server.close())

describe('Feedback 12.11: source chips open the test document (R69)', () => {
  it('a click opens the document with the test badge; Escape closes it; external links stay links', async () => {
    signInAs('intern.anna')
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <SourceChips
          sources={[
            { doc_id: 'common-laptop', title: 'Ноутбук и техника', source_type: 'test', url: '' },
            { doc_id: 'w1', title: 'Git docs', source_type: 'external', url: 'https://git-scm.com/docs' },
          ]}
        />
      </QueryClientProvider>,
    )
    expect(screen.getByRole('link', { name: 'Git docs' })).toHaveAttribute('target', '_blank')
    await userEvent.click(screen.getByRole('button', { name: 'Ноутбук и техника' }))
    const drawer = await screen.findByRole('dialog', { name: 'Ноутбук и техника' })
    expect(await screen.findByText(/текст документа/)).toBeInTheDocument()
    expect(drawer).toHaveTextContent('Тестовая инструкция прототипа')
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('a mentor answer added to the KB is marked as such', () => {
    signInAs('intern.anna')
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <SourceChips sources={[{ doc_id: 'faq-delivery-1a2b3c4d', title: 'Где логи стенда', source_type: 'test', url: '' }]} />
      </QueryClientProvider>,
    )
    expect(screen.getByRole('button', { name: 'Ответ наставника: Где логи стенда' })).toBeInTheDocument()
  })
})
