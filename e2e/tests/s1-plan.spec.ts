import { expect, test } from '@playwright/test'
import { ask, lastTurn, login, newDialog, waitAnswer } from './helpers'

// S1: план из чек-листа Авито, статусы, следующий шаг, цели от наставника, напоминания.
test('S1: intern sees the plan with periods, next step and toggles a task in under a second', async ({ page }) => {
  await login(page, 'intern.anna')
  await page.getByRole('link', { name: 'Мой план' }).click()
  await expect(page.getByRole('heading', { name: 'План адаптации' })).toBeVisible()
  await expect(page.getByRole('progressbar', { name: 'Прогресс плана' })).toBeVisible()
  await expect(page.getByRole('list', { name: 'Этапы стажировки' }).locator('[aria-current="step"]')).toBeVisible()
  await expect(page.getByLabel('Следующий шаг')).toBeVisible()

  // R76: простая локальная операция отвечает быстрее 1 с (оптимистичное переключение), и состояние сохраняется после
  // перезагрузки
  const candidate = page.locator('.plan-subtasks label').filter({ has: page.locator('input:not(:checked):not(:disabled)') }).first()
  const label = await candidate.innerText()
  // стабильный локатор по тексту: локатор с `:not(:checked)` после клика перепрыгнул бы на следующую галочку
  const box = page.locator('.plan-subtasks label').filter({ hasText: label }).locator('input')
  const t0 = Date.now()
  await box.click()
  await expect(box).toBeChecked({ timeout: 1000 })
  expect(Date.now() - t0).toBeLessThan(1000)
  await page.waitForLoadState('networkidle')
  await page.reload()
  const again = page.locator('.plan-subtasks label').filter({ hasText: label }).locator('input')
  await expect(again).toBeChecked()
  await again.click() // вернуть как было для следующего запуска
  await expect(again).not.toBeChecked()
})

test('S1: «что дальше?» in chat answers with the period and a next step card', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'что дальше?')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Сейчас этап')
  const card = page.getByLabel('Следующий шаг').last()
  await expect(card).toBeVisible()
  await card.getByRole('button').first().click()
  await waitAnswer(page)
  await expect(page.locator('.chat-row-user').last()).not.toBeEmpty()
})

test('S1: mentor sees interns, opens a plan read-only and sets goals that the intern sees', async ({ browser }) => {
  const goal = `Выкатить первую задачу (${Date.now() % 100000})`
  const mentor = await (await browser.newContext()).newPage()
  await login(mentor, 'mentor.oleg')
  await mentor.getByRole('link', { name: 'Мои стажёры' }).click()
  await mentor.getByRole('link', { name: 'План: Анна Котова' }).click()
  await expect(mentor.getByText('Цели адаптации')).toBeVisible()
  await expect(mentor.locator('.plan-task input[type="checkbox"]').first()).toBeDisabled()
  await mentor.getByRole('button', { name: /Задать цели|Изменить цели/ }).click()
  await mentor.getByLabel('Цель 1').fill(goal)
  await mentor.getByLabel('Критерий 1').fill('Задача в проде, MR смержен')
  await mentor.getByRole('button', { name: 'Сохранить' }).click()
  await expect(mentor.getByText(goal)).toBeVisible()

  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.anna')
  await intern.goto('/plan')
  await expect(intern.getByText(goal)).toBeVisible()
  // стажёр не редактирует и не утверждает цели; она может только записать новые на утверждение (организаторы 12.11)
  await expect(intern.getByRole('button', { name: /Изменить цели|Задать цели|Согласовать/ })).toHaveCount(0)
})

test('S1: reminders arrive in the notifications bell', async ({ page }) => {
  await login(page, 'intern.anna')
  await page.getByRole('button', { name: /Уведомления/ }).click()
  await expect(page.getByRole('region', { name: 'Уведомления' })).toBeVisible()
})

test('Report: the mentor opens the intern report; the intern has no report (idea 12.11)', async ({ browser }) => {
  const mentor = await (await browser.newContext()).newPage()
  await login(mentor, 'mentor.oleg')
  await mentor.goto('/team/intern.anna')
  await mentor.getByRole('link', { name: 'Отчёт за стажировку' }).click()
  await expect(mentor.getByRole('heading', { name: 'Отчёт за стажировку: Анна Котова' })).toBeVisible()
  const tasks = mentor.getByLabel('Задачи плана')
  await expect(tasks).toContainText(/Задачи плана: выполнено \d+ из \d+/)
  await expect(tasks).toContainText('раньше срока')
  await expect(mentor.getByText(/Промежуточный отчёт: день \d+ из 90/)).toBeVisible()
  await expect(mentor.getByLabel('Что обсудить на 1:1')).toContainText(/Просрочена задача|Просрочек нет/)

  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.anna')
  await intern.goto('/plan')
  await expect(intern.getByRole('heading', { name: 'План адаптации' })).toBeVisible()
  await expect(intern.getByRole('link', { name: 'Отчёт за стажировку' })).toHaveCount(0)
  await intern.goto('/team/intern.anna/report')
  await expect(intern).toHaveURL(/\/chat/)
})

test('Goals: the intern writes down the goals after the meeting, the lead approves them (organizers 12.11)', async ({ browser }) => {
  const title = `Выкатить первую задачу ${Date.now() % 100000}`
  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.anna')
  await intern.goto('/plan')
  const goals = intern.getByRole('region', { name: 'Цели адаптации' })
  await goals.getByRole('button', { name: 'Записать цели после встречи' }).click()
  await goals.getByLabel('Цель 1').fill(title)
  await goals.getByLabel('Критерий 1').fill('PR влит, фича за флагом')
  await goals.getByRole('button', { name: 'Отправить на согласование' }).click()
  await expect(goals.getByText(title)).toBeVisible()
  await expect(goals.getByText('Ждёт согласования').first()).toBeVisible()

  const lead = await (await browser.newContext()).newPage()
  await login(lead, 'lead.maria')
  await lead.goto('/team/intern.anna')
  const card = lead.getByRole('region', { name: 'Цели адаптации' }).locator('.plan-goal').filter({ hasText: title })
  await card.getByRole('button', { name: 'Согласовать' }).click()
  await expect(card.getByText('В работе')).toBeVisible()
  await card.getByRole('button', { name: 'Достигнута', exact: true }).click()
  await expect(lead.getByRole('region', { name: 'Цели адаптации' }).locator('.plan-goal').filter({ hasText: title }).getByText('Достигнута', { exact: true })).toBeVisible()
})

test('Confirmation: a study task opens a mini-check, a team task is confirmed by the mentor (organizers 12.13)', async ({ browser }) => {
  const ivan = await (await browser.newContext()).newPage()
  await login(ivan, 'intern.ivan')
  await ivan.goto('/plan')
  const study = ivan.locator('.plan-task').filter({ hasText: 'Изучи все необходимые документы' })
  await expect(study).toContainText('отмечается после мини-проверки')
  await expect(study.getByRole('checkbox')).toBeDisabled()
  const team = ivan.locator('.plan-task').filter({ hasText: 'Познакомься с командой' })
  // повторный запуск находит задачу выполненной или ожидающей: сначала вернуть её в работу
  if (await team.getByRole('checkbox').isChecked()) await team.getByRole('checkbox').click()
  await expect(team.getByRole('checkbox')).not.toBeChecked()
  if (!(await team.textContent())?.includes('Ждёт подтверждения')) await team.getByRole('checkbox').click() // спрашивает наставника
  await expect(team).toContainText('Ждёт подтверждения')

  const sergey = await (await browser.newContext()).newPage()
  await login(sergey, 'mentor.sergey')
  await sergey.goto('/team/intern.ivan')
  const card = sergey.locator('.plan-task').filter({ hasText: 'Познакомься с командой' })
  await card.getByRole('button', { name: 'Подтвердить выполнение' }).click()
  await expect(card).toContainText('Готово')

  await study.getByRole('button', { name: 'Проверить себя' }).click()
  await expect(ivan).toHaveURL(/\/chat/)
  await expect(lastTurn(ivan)).toContainText('Вставь пропущенные слова', { timeout: 20000 })
})
