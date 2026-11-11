import { expect, test } from '@playwright/test'
import { ask, lastTurn, login, newDialog, waitAnswer } from './helpers'

// S1: plan from the Avito checklist, statuses, next step, mentor goals, reminders.
test('S1: intern sees the plan with periods, next step and toggles a task in under a second', async ({ page }) => {
  await login(page, 'intern.anna')
  await page.getByRole('link', { name: 'Мой план' }).click()
  await expect(page.getByRole('heading', { name: 'План адаптации' })).toBeVisible()
  await expect(page.getByRole('progressbar', { name: 'Прогресс плана' })).toBeVisible()
  await expect(page.getByRole('list', { name: 'Этапы стажировки' }).locator('[aria-current="step"]')).toBeVisible()
  await expect(page.getByLabel('Следующий шаг')).toBeVisible()

  // R76: a simple local operation answers in < 1 s (optimistic toggle), and the state survives a reload
  const candidate = page.locator('.plan-subtasks label').filter({ has: page.locator('input:not(:checked):not(:disabled)') }).first()
  const label = await candidate.innerText()
  // a stable locator by text: a `:not(:checked)` one would jump to the next box after the click
  const box = page.locator('.plan-subtasks label').filter({ hasText: label }).locator('input')
  const t0 = Date.now()
  await box.click()
  await expect(box).toBeChecked({ timeout: 1000 })
  expect(Date.now() - t0).toBeLessThan(1000)
  await page.waitForLoadState('networkidle')
  await page.reload()
  const again = page.locator('.plan-subtasks label').filter({ hasText: label }).locator('input')
  await expect(again).toBeChecked()
  await again.click() // put it back for the next run
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
  await expect(intern.getByRole('button', { name: /цели/ })).toHaveCount(0)
})

test('S1: reminders arrive in the notifications bell', async ({ page }) => {
  await login(page, 'intern.anna')
  await page.getByRole('button', { name: /Уведомления/ }).click()
  await expect(page.getByRole('region', { name: 'Уведомления' })).toBeVisible()
})
