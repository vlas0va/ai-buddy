// Скриншоты для слайдов «Командировки и согласия» и «Файлы и поиск». Нужен стек на :5173 (stub-LLM).
const { chromium } = require('playwright');
const OUT = __dirname + '/img/';
const W = process.env.WEB_URL || 'http://127.0.0.1:5173';
(async () => {
  const b = await chromium.launch();
  const mk = async () => (await b.newContext({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2, locale: 'ru-RU' })).newPage();
  const login = async (p, u) => { await p.goto(W + '/login'); await p.getByLabel('Логин').fill(u); await p.getByLabel('Пароль', { exact: true }).fill('demo'); await p.getByRole('button', { name: 'Войти' }).click(); await p.waitForURL('**/chat'); };
  const idle = async (p) => { await p.waitForTimeout(300); await p.waitForFunction(() => !document.querySelector('textarea[aria-label="Сообщение"]')?.disabled, null, { timeout: 30000 }); await p.waitForTimeout(400); };
  const ask = async (p, t) => { const i = p.getByLabel('Сообщение'); await i.fill(t); await i.press('Enter'); await idle(p); };
  const fresh = async (p) => { await p.getByRole('button', { name: 'Новый диалог' }).click(); await p.waitForTimeout(300); };
  const fmt = (d) => `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}.${d.getFullYear()}`;
  const s = new Date(); s.setDate(s.getDate() + 40); const e = new Date(s); e.setDate(e.getDate() + 2);

  const a = await mk(); await login(a, 'intern.anna');
  await fresh(a); await ask(a, `оформи командировку в Казань с ${fmt(s)} по ${fmt(e)}, цель — встреча с командой поиска`);
  await a.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click();
  await a.getByLabel('Результат заявки').last().waitFor(); await a.waitForTimeout(800);
  await a.screenshot({ path: OUT + 'ex-trip.png' });
  await fresh(a); await ask(a, 'какие у меня согласия?'); await ask(a, 'подпиши согласие на опросы');
  await a.screenshot({ path: OUT + 'ex-consent.png' });
  await fresh(a);
  await a.getByTestId('chat-file-input').setInputFiles({ name: 'app.log', mimeType: 'text/plain',
    buffer: Buffer.from('2026-11-11 10:00:01 ERROR connect postgres://buddy:hunter2@db:5432/app failed\nconnection refused\n') });
  await a.locator('form.chat-input').getByRole('button', { name: 'Отправить' }).click(); await idle(a);
  await a.screenshot({ path: OUT + 'ex-file.png' });
  await fresh(a); await ask(a, 'чем rebase отличается от merge?');
  await a.screenshot({ path: OUT + 'ex-web.png' });
  await b.close();
})().catch((err) => { console.error(err); process.exit(1); });
