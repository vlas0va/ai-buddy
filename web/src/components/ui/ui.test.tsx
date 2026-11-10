import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Avatar, Badge, Button, ErrorState, Field, initials } from '.'

describe('ui kit', () => {
  it('Button loading disables and marks busy', () => {
    render(<Button loading>Войти</Button>)
    const b = screen.getByRole('button', { name: 'Войти' })
    expect(b).toBeDisabled()
    expect(b).toHaveAttribute('aria-busy', 'true')
  })

  it('Field binds label and error', () => {
    render(<Field label="Логин" error="Обязательное поле" />)
    const input = screen.getByLabelText('Логин')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription('Обязательное поле')
  })

  it('initials', () => {
    expect(initials('intern.anna')).toBe('IA')
    expect(initials('Анна')).toBe('АН')
    expect(initials('  ')).toBe('?')
    render(<Avatar name="Анна Иванова" />)
    expect(screen.getByText('АИ')).toBeInTheDocument()
  })

  it('Badge test variant has default text', () => {
    render(<Badge variant="test" />)
    expect(screen.getByText('Тестовая инструкция прототипа')).toBeInTheDocument()
  })

  it('ErrorState retry', async () => {
    const onRetry = vi.fn()
    render(<ErrorState message="Сбой" onRetry={onRetry} />)
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
