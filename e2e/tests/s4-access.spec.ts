import { expect, test } from '@playwright/test'
import { ask, lastTurn, login, newDialog, waitAnswer } from './helpers'

// S4: required accesses with statuses, a request through access MCP, channels and a draft to the mentor.
test('S4: required accesses are listed with statuses; a missing one is requested and saved', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'какие доступы мне нужны?')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Confluence')
  await expect(lastTurn(page)).toContainText('Bitbucket')
  const draft = page.getByLabel('Черновик заявки').last()
  if (await draft.count()) {
    await draft.getByRole('button', { name: 'Отправить' }).click()
    const result = page.getByLabel('Результат заявки').last()
    await expect(result).toContainText(/ACC-\d+/)
    await expect(result).toContainText('Сохранено в журнале действий')
  } else {
    await expect(lastTurn(page)).toContainText(/уже|запрошен/)
  }
})

test('S4: channels of the team come with a draft to the mentor', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'в какие каналы мне вступить?')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('#delivery')
  await expect(page.getByLabel('Черновик сообщения').last()).toBeVisible()
})

test('S4: a clone question is engineering help, not an access request', async ({ page }) => {
  await login(page, 'intern.ivan')
  await newDialog(page)
  await ask(page, 'как склонировать репозиторий команды?')
  await waitAnswer(page)
  await expect(lastTurn(page).getByLabel('Источники')).toBeVisible()
  await expect(lastTurn(page).getByLabel('Черновик заявки')).toHaveCount(0)
})
