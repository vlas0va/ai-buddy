import { expect, test } from '@playwright/test'
import { ask, ddmmyyyy, lastTurn, login, newDialog, waitAnswer } from './helpers'

// Organizers' Q&A 10.11 and «Пространство решений»: business trips, consents, HR steps of a day off, files in chat and
// web search for general technical questions (openspec trips-consents, attachments-websearch).

test('Trip: one message → draft → TRP-… → the lead approves it in «Согласования»', async ({ browser }) => {
  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.ivan')
  await newDialog(intern)
  const start = new Date()
  start.setHours(12, 0, 0, 0)
  start.setDate(start.getDate() + 30 + (Math.floor(Date.now() / 1000) % 200)) // a fresh date on every run
  const end = new Date(start)
  end.setDate(end.getDate() + 1)
  await ask(intern, `оформи командировку в Казань с ${ddmmyyyy(start)} по ${ddmmyyyy(end)}, цель — встреча с командой`)
  await waitAnswer(intern)
  const draft = intern.getByLabel('Черновик заявки').last()
  await expect(draft).toContainText('Черновик заявки на командировку')
  await expect(draft).toContainText('Казань')
  await expect(draft).toContainText('Дмитрий Волков')
  await draft.getByRole('button', { name: 'Отправить' }).click()
  const result = intern.getByLabel('Результат заявки').last()
  await expect(result).toContainText(/TRP-\d+/)
  await expect(result).toContainText('На согласовании')
  const id = (await result.locator('h3').innerText()).match(/TRP-\d+/)![0]

  const lead = await (await browser.newContext()).newPage()
  await login(lead, 'lead.dmitry')
  await lead.getByRole('link', { name: 'Согласования', exact: true }).click()
  const card = lead.getByLabel('Заявка: Иван Белов').filter({ hasText: 'Командировка' }).filter({ hasText: ddmmyyyy(start) }).first()
  await expect(card).toBeVisible()
  await card.getByRole('button', { name: 'Согласовать' }).click()
  await expect(card).toHaveCount(0)

  await intern.reload()
  const panel = intern.getByRole('complementary', { name: 'Мои заявки' })
  await expect(panel.locator('li, .ui-card').filter({ hasText: id }).first()).toContainText('Согласовано')
})

// Other accounts than intern.anna: the chat rate limit (20 messages a minute per user) is shared with other specs.
test('Trip: own city is explained, booking a hotel is refused', async ({ page }) => {
  await login(page, 'mentor.oleg')
  await newDialog(page)
  await ask(page, 'командировка в Москву на следующей неделе, цель — встреча')
  await waitAnswer(page)
  await expect(page.getByLabel('Черновик заявки')).toHaveCount(0)
  await ask(page, 'забронируй мне гостиницу в Казани')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('служба командировок')
})

test('Consents: statuses are listed, a voluntary consent is signed after «Отправить»', async ({ page }) => {
  await login(page, 'intern.ivan')
  await newDialog(page)
  await ask(page, 'какие у меня согласия?')
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Обработка персональных данных')
  await ask(page, 'подпиши согласие на опросы')
  await waitAnswer(page)
  const draft = page.getByLabel('Черновик заявки').last()
  if (await lastTurn(page).getByLabel('Черновик заявки').count()) {
    await expect(draft).toContainText('Подпись согласия')
    await draft.getByRole('button', { name: 'Отправить' }).click()
    const result = page.getByLabel('Результат заявки').last()
    await expect(result).toContainText(/CNS-\d+/)
    await expect(result).toContainText('Подписано')
  } else {
    await expect(lastTurn(page)).toContainText('уже подписано') // a repeated run
  }
})

test('Day off: the approved request shows the HR admin and HR BP steps', async ({ browser }) => {
  const intern = await (await browser.newContext()).newPage()
  await login(intern, 'intern.anna')
  await newDialog(intern)
  const day = new Date()
  day.setHours(12, 0, 0, 0)
  day.setDate(day.getDate() + 90 + (Math.floor(Date.now() / 1000) % 120))
  while (day.getDay() === 0 || day.getDay() === 6) day.setDate(day.getDate() + 1)
  await ask(intern, `хочу отгул ${ddmmyyyy(day)}`)
  await waitAnswer(intern)
  await intern.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click()
  const result = intern.getByLabel('Результат заявки').last()
  await expect(result).toContainText('HR-администратор: согласует после руководителя')
  const id = (await result.locator('h3').innerText()).match(/ABS-\d+/)![0]

  const lead = await (await browser.newContext()).newPage()
  await login(lead, 'lead.maria')
  await lead.getByRole('link', { name: 'Согласования', exact: true }).click()
  const card = lead.getByLabel('Заявка: Анна Котова').filter({ hasText: ddmmyyyy(day).slice(0, 5) }).first()
  await card.getByRole('button', { name: 'Согласовать' }).click()
  await expect(card).toHaveCount(0)

  const admin = await (await browser.newContext()).newPage()
  await login(admin, 'hradmin.olga')
  await admin.getByRole('link', { name: 'Согласования', exact: true }).click()
  const hrCard = admin.getByLabel('Заявка: Анна Котова').filter({ hasText: ddmmyyyy(day).slice(0, 5) }).first()
  await hrCard.getByRole('button', { name: 'Согласовать' }).click()
  await expect(hrCard).toHaveCount(0)

  await intern.reload()
  const panel = intern.getByRole('complementary', { name: 'Мои заявки' })
  const item = panel.locator('li').filter({ hasText: id }).first()
  await expect(item).toContainText('HR-администратор: оформил в кадровой системе')
  await expect(item).toContainText('HR BP: уведомлён')
})

test('Files: a log is masked and explained, a screenshot is not guessed', async ({ page }) => {
  await login(page, 'mentor.sergey')
  await newDialog(page)
  const input = page.getByTestId('chat-file-input')
  await input.setInputFiles({
    name: 'app.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('ERROR connect postgres://buddy:hunter2@db:5432/app\nconnection refused\n'),
  })
  await expect(page.getByLabel('Прикреплённые файлы')).toContainText('app.log')
  await page.locator('form.chat-input').getByRole('button', { name: 'Отправить' }).click()
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('Главная строка ошибки')
  await expect(page.locator('.chat-row-buddy').last()).not.toContainText('hunter2')
  await expect(page.getByLabel('Вложения').last()).toContainText('app.log')

  await input.setInputFiles({ name: 'screen.png', mimeType: 'image/png', buffer: Buffer.from([137, 80, 78, 71]) })
  await page.locator('form.chat-input').getByRole('button', { name: 'Отправить' }).click()
  await waitAnswer(page)
  await expect(lastTurn(page)).toContainText('не распознаю')
})

test('Web search: a general technical question gets external sources, never the KB badge', async ({ page }) => {
  await login(page, 'hrbp.elena')
  await newDialog(page)
  await ask(page, 'чем rebase отличается от merge?')
  await waitAnswer(page)
  const web = lastTurn(page).getByLabel('Внешние источники')
  await expect(web).toContainText('Внешний источник')
  await expect(web.getByRole('link').first()).toHaveAttribute('href', /^https:\/\//)
  await expect(lastTurn(page)).toContainText('не правила Авито')
})

test('Vacation: Labour Code facts and the process, then a vacation draft with its type', async ({ page }) => {
  await login(page, 'mentor.sergey')
  await newDialog(page)
  await ask(page, 'сколько дней отпуска положено и как взять отпуск?')
  await waitAnswer(page)
  const answer = lastTurn(page)
  await expect(answer).toContainText('28 календарных дней')
  await expect(answer).toContainText('HR-администратор')
  const start = new Date()
  start.setHours(12, 0, 0, 0)
  start.setDate(start.getDate() + 200 + (Math.floor(Date.now() / 1000) % 100))
  const end = new Date(start)
  end.setDate(end.getDate() + 6)
  await ask(page, `хочу в отпуск с ${ddmmyyyy(start)} по ${ddmmyyyy(end)}`)
  await waitAnswer(page)
  const draft = page.getByLabel('Черновик заявки').last()
  await expect(draft).toContainText('Отпуск')
  await expect(draft).toContainText('Календарных дней')
})
