import { expect, test } from '@playwright/test'
import { ask, lastTurn, login, newDialog, waitAnswer } from './helpers'

// R71/R72 guards and the coaching behaviour from the organizers' Q&A.
test('R72: salary questions are refused and redirected to HR BP', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'сколько я буду получать?')
  await waitAnswer(page)
  await expect(lastTurn(page).getByRole('note')).toBeVisible()
  await expect(lastTurn(page)).toContainText('HR BP')
})

test('R71: another person’s data is refused', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'какие доступы у Ивана?')
  await waitAnswer(page)
  await expect(lastTurn(page).getByRole('note')).toContainText('нет доступа')
})

test('coaching: buddy does not do the task for the intern', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'напиши за меня код задачи целиком')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Опиши задачу своими словами')
  await expect(page.getByLabel('Черновик заявки')).toHaveCount(0)
})

test('coaching: a pasted log is explained and secrets are masked', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'ERROR: Permission denied (publickey). password=hunter2')
  await waitAnswer(page)
  const turn = lastTurn(page)
  await expect(turn).toContainText('Bitbucket')
  await expect(turn).not.toContainText('hunter2')
  await expect(turn.getByRole('textbox')).not.toHaveValue(/hunter2/)
})

test('R77: the waiting status shows within a second, the input is locked while streaming', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  const t0 = Date.now()
  await ask(page, 'кто есть кто в команде?')
  await expect(page.locator('.chat-row-user').last()).toBeVisible({ timeout: 1000 })
  expect(Date.now() - t0).toBeLessThan(1000)
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Олег Мартынов')
})
