import { expect, test } from '@playwright/test'
import { ask, ddmmyyyy, futureTuesday, lastTurn, login, newDialog, waitAnswer } from './helpers'

// S3 (main scenario): procedure → dates → draft → confirm → absence MCP → id/status → saved → lead approves → status.
test('S3: day off from question to lead approval, visible after reload', async ({ browser }) => {
  const day = ddmmyyyy(futureTuesday())
  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.ivan')
  await newDialog(intern)

  await ask(intern, 'Как оформить отгул?')
  await waitAnswer(intern)
  await expect(lastTurn(intern).getByLabel('Источники')).toBeVisible()
  await expect(lastTurn(intern).getByText('Тестовая инструкция прототипа')).toBeVisible()
  await expect(intern.getByLabel('Черновик заявки')).toHaveCount(0) // a question never creates a request

  await ask(intern, `хочу отгул ${day}`)
  await waitAnswer(intern)
  const draft = intern.getByLabel('Черновик заявки').last()
  await expect(draft).toBeVisible()
  await expect(draft).toContainText(day.slice(0, 5).replace(/^0/, ''))
  await draft.getByRole('button', { name: 'Отправить' }).click()

  const result = intern.getByLabel('Результат заявки').last()
  await expect(result).toContainText(/ABS-\d+/)
  await expect(result).toContainText('На согласовании')
  await expect(result).toContainText('Сохранено в журнале действий')
  const id = (await result.locator('h3').innerText()).match(/ABS-\d+/)![0]

  await intern.reload() // R66: the action survives a reload
  await expect(intern.getByRole('complementary', { name: 'Мои заявки' })).toContainText(id)

  const lead = await (await browser.newContext()).newPage()
  await login(lead, 'lead.dmitry')
  await lead.getByRole('link', { name: 'Согласования', exact: true }).click()
  const card = lead.getByLabel('Заявка: Иван Белов').filter({ hasText: day.slice(0, 5) }).first()
  await expect(card).toBeVisible()
  await card.getByRole('button', { name: 'Согласовать' }).click()
  await expect(card).toHaveCount(0)

  // checkpoint 11.11: after the lead, the HR admin approves the request
  await intern.reload()
  const panel = intern.getByRole('complementary', { name: 'Мои заявки' })
  await expect(panel.locator('li, .ui-card').filter({ hasText: id }).first()).toContainText('Ждёт HR-администратора')
  const admin = await (await browser.newContext()).newPage()
  await login(admin, 'hradmin.olga')
  await admin.getByRole('link', { name: 'Согласования', exact: true }).click()
  const hrCard = admin.getByLabel('Заявка: Иван Белов').filter({ hasText: day.slice(0, 5) }).first()
  await expect(hrCard).toContainText('Этап: HR-администратор')
  await hrCard.getByRole('button', { name: 'Согласовать' }).click()
  await expect(hrCard).toHaveCount(0)

  await intern.reload()
  await expect(panel.locator('li, .ui-card').filter({ hasText: id }).first()).toContainText('Согласовано')
})

test('S3: invalid dates are explained, nothing is drafted', async ({ page }) => {
  await login(page, 'intern.ivan')
  await newDialog(page)
  await ask(page, 'хочу отгул вчера')
  await waitAnswer(page)
  await expect(page.getByLabel('Черновик заявки')).toHaveCount(0)
  await expect(lastTurn(page).locator('.chat-bubble')).not.toBeEmpty()
})
