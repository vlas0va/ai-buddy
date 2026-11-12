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
  // feedback 12.11: the missing accesses are one list with checkboxes; ordinary ones are checked
  const list = page.getByLabel('Набор заявок на доступ').last()
  if (await list.count()) {
    await list.getByRole('button', { name: /Запросить выбранные/ }).click()
    // results land in the rows of the list; every request is saved and listed in «Мои заявки» (R66)
    await expect(list).toContainText(/выдан · ACC-\d+/)
    await expect(list).toContainText('Готово', { timeout: 20000 })
    await page.reload()
    await expect(page.getByLabel('Набор заявок на доступ').last()).toContainText(/выдан · ACC-\d+/)
    await expect(page.getByRole('complementary', { name: 'Мои заявки' })).toContainText(/ACC-\d+/)
  } else {
    await expect(lastTurn(page)).toContainText(/уже|запрошен/)
  }
})

// checkpoint 11.11: adding to groups is asked in personal messages, so buddy gives advice without a draft
test('S4: channels of the team come with advice to write the mentor personally', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'в какие каналы мне вступить?')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('#delivery')
  await expect(lastTurn(page)).toContainText('личные сообщения')
  await expect(lastTurn(page).getByLabel('Черновик сообщения')).toHaveCount(0)
})

test('S4: a clone question is engineering help, not an access request', async ({ page }) => {
  await login(page, 'intern.ivan')
  await newDialog(page)
  await ask(page, 'как склонировать репозиторий команды?')
  await waitAnswer(page)
  await expect(lastTurn(page).getByLabel('Источники')).toBeVisible()
  await expect(lastTurn(page).getByLabel('Черновик заявки')).toHaveCount(0)
})

// checkpoint 11.11: the accesses are a table; a request names the resource, the environment and the rights
test('S4: accesses table and a database request on stage for reading', async ({ page }) => {
  await login(page, 'mentor.oleg')
  await newDialog(page)
  await ask(page, 'какие доступы мне нужны?')
  await waitAnswer(page)
  await expect(lastTurn(page).locator('table')).toContainText('Jira: проект DLV')
  await expect(lastTurn(page).locator('table')).toContainText('dev, stage, prod')
  await ask(page, 'дай доступ к БД на stage на чтение')
  await waitAnswer(page)
  const draft = page.getByLabel('Черновик заявки').last()
  await expect(draft).toContainText('БД delivery_orders')
  await expect(draft).toContainText('stage')
  await expect(draft).toContainText('чтение')
})
