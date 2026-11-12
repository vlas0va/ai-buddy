import { expect, test } from '@playwright/test'
import { ask, lastTurn, login, newDialog, waitAnswer } from './helpers'

// S2: answers with team context and a test source; unknown → escalation draft → mentor answers.
test('S2: team-specific answer with a source and the test badge', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'Как у нас проходит код-ревью?')
  await waitAnswer(page)
  const turn = lastTurn(page)
  await expect(turn.getByLabel('Источники')).toContainText('Доставка')
  await expect(turn.getByText('Тестовая инструкция прототипа')).toBeVisible()
  await expect(turn.locator('.chat-bubble')).not.toContainText('##')
  await expect(turn.getByRole('group', { name: 'Оценка ответа' })).toBeVisible()
  await turn.getByRole('button', { name: 'Полезно', exact: true }).click()
  await expect(turn.getByText('Спасибо за оценку')).toBeVisible()
})

test('S2: engineering onboarding — branches and PR answered from the KB', async ({ page }) => {
  await login(page, 'intern.anna')
  await newDialog(page)
  await ask(page, 'как создать ветку для задачи?')
  await waitAnswer(page)
  await expect(lastTurn(page).getByLabel('Источники')).toContainText('Ветки, коммиты и pull request')
})

test('S2: unknown question → draft to the mentor → mentor answers → intern sees the answer', async ({ browser }) => {
  const marker = `Где посмотреть расписание командного митапа по архитектуре в этом квартале (${Date.now() % 100000})?`
  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.anna')
  await newDialog(intern)
  await ask(intern, marker)
  await waitAnswer(intern)
  const draft = intern.getByLabel('Черновик сообщения').last()
  await expect(draft).toBeVisible()
  await draft.getByRole('button', { name: /Отправить/ }).click()
  await expect(draft.getByText('Сообщение отправлено.')).toBeVisible()
  await intern.getByRole('link', { name: 'Мои вопросы' }).click()
  await expect(intern.getByText(marker).first()).toBeVisible()

  const mentor = await (await browser.newContext()).newPage()
  await login(mentor, 'mentor.oleg')
  await mentor.getByRole('link', { name: 'Вопросы стажёров' }).click()
  const card = mentor.locator('.esc-card').filter({ hasText: marker }).first()
  await card.getByRole('textbox').fill('Напиши в #delivery-team, стенд выдаёт Наталья Фёдорова.')
  await card.getByRole('button', { name: 'Ответить' }).click()
  await expect(card.getByText('Есть ответ')).toBeVisible()

  await intern.reload()
  await expect(intern.locator('.esc-card').filter({ hasText: marker }).first()).toContainText('Наталья Фёдорова')
})

test('S2: a question the KB does not know → Oleg answers for all → another intern gets his answer (feedback 12.13)', async ({ browser }) => {
  const nonce = String(Date.now() % 1e6).replace(/\d/g, (d) => 'абвгдежзик'[Number(d)])
  const question = `Можно ли приходить в офис с собакой породы ${nonce}?`

  const anna = await (await browser.newContext()).newPage()
  await login(anna, 'intern.anna')
  await newDialog(anna)
  await ask(anna, question)
  await waitAnswer(anna)
  await expect(lastTurn(anna)).toContainText('Точного ответа в базе знаний нет')
  const draft = anna.getByLabel('Черновик сообщения').last()
  await draft.getByRole('button', { name: /Отправить/ }).click()
  await expect(lastTurn(anna)).toContainText('Сообщение отправлено')

  const oleg = await (await browser.newContext()).newPage()
  await login(oleg, 'mentor.oleg')
  await oleg.goto('/questions')
  const card = oleg.locator('.esc-card').filter({ hasText: nonce }).first()
  await card.getByRole('textbox').fill('Нет, в офис с животными нельзя, кроме собак-поводырей.')
  await card.getByLabel(/Добавить ответ в базу знаний/).check()
  await card.getByLabel(/Всем стажёрам/).check()
  await card.getByRole('button', { name: 'Ответить' }).click()
  await expect(oleg.locator('.esc-card').filter({ hasText: nonce }).first()).toContainText('Ответ добавлен в базу знаний')

  const ivan = await (await browser.newContext()).newPage() // another team: the answer was added for all
  await login(ivan, 'intern.ivan')
  await newDialog(ivan)
  await ask(ivan, question)
  await waitAnswer(ivan)
  await expect(lastTurn(ivan)).toContainText('собак-поводырей')
  await expect(lastTurn(ivan)).toContainText('Ответ наставника')
})
