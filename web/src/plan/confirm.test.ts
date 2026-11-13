import { describe, expect, it } from 'vitest'
import type { Task } from '@/api/schemas'
import { askLabel, askText, canToggle, nextStatus } from './format'

const task = (over: Partial<Task>): Task => ({
  id: 't', source: 'checklist', checklist_item_id: 'CL-07', period: 'week_2', title: 'Изучи документы команды',
  status: 'in_progress', human_only: false, ...over,
})

describe('Organizers 12.13: who confirms a task', () => {
  it('a quiz task is not ticked by hand: «Проверить себя» starts the mini-check in the chat', () => {
    const t = task({ confirm: 'quiz' })
    expect(canToggle(t)).toBe(false)
    expect(canToggle({ ...t, status: 'done' })).toBe(true) // можно переоткрыть
    expect(askLabel(t)).toBe('Проверить себя')
    expect(askText(t)).toBe('Проверь меня по задаче «Изучи документы команды»')
  })

  it('a mentor task: the click asks the mentor, a second click takes the request back', () => {
    const t = task({ confirm: 'mentor', checklist_item_id: 'CL-03' })
    expect(nextStatus(t)).toBe('waiting_approval')
    expect(canToggle({ ...t, status: 'waiting_approval' })).toBe(true)
    expect(nextStatus({ ...t, status: 'waiting_approval' })).toBe('in_progress')
  })

  it('an ordinary task is ticked by the intern; a request task changes by itself', () => {
    expect(nextStatus(task({ confirm: 'self' }))).toBe('done')
    expect(canToggle(task({ source: 'action', status: 'waiting_approval' }))).toBe(false)
  })
})
