// Синтетические тестовые учётные записи, docs/context/product.md. Пароли только для локального прототипа.
export const TEST_ACCOUNTS = [
  { login: 'intern.anna', label: 'Стажёр, команда «Доставка»' },
  { login: 'intern.ivan', label: 'Стажёр, команда «Поиск»' },
  { login: 'mentor.oleg', label: 'Наставник' },
  { login: 'lead.maria', label: 'Руководитель' },
  { login: 'hrbp.elena', label: 'HR BP' },
  { login: 'hradmin.olga', label: 'HR-администратор' },
  { login: 'alumni.petr', label: 'Выпускник стажировки' },
] as const

export const TEST_PASSWORD = 'demo'

const byLogin = (...logins: string[]) => TEST_ACCOUNTS.filter((a) => logins.includes(a.login))

export const TEST_ACCOUNT_GROUPS = [
  { title: 'Стажёры', accounts: byLogin('intern.anna', 'intern.ivan') },
  { title: 'Команда', accounts: byLogin('mentor.oleg', 'lead.maria') },
  { title: 'Другие роли', accounts: byLogin('hradmin.olga', 'hrbp.elena', 'alumni.petr') },
]
