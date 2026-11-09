import { useMutation } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { login } from '@/api/auth'
import { ApiError, errorMessage } from '@/api/client'
import { Avatar } from '@/components/ui/Avatar'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Field } from '@/components/ui/Input'
import { Logo } from '@/components/ui/Logo'
import { EyeIcon, EyeOffIcon } from '@/components/ui/icons'
import { useSession } from '@/store/session'
import '@/components/layout.css'
import { LoginHero } from './LoginHero'
import { TEST_ACCOUNT_GROUPS, TEST_PASSWORD } from './accounts'

const SERVER_DOWN = 'Сервер недоступен. Проверьте, что запущен бэкенд, или включите демо-режим.'

function loginErrorText(e: unknown): string {
  return e instanceof ApiError && e.status === 0 ? SERVER_DOWN : errorMessage(e)
}

export function LoginPage({ onLoggedIn }: { onLoggedIn: () => void }) {
  const setSession = useSession((s) => s.setSession)
  const [loginName, setLoginName] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)

  const mutation = useMutation({
    mutationFn: (creds: { login: string; password: string }) => login(creds.login.trim(), creds.password),
    onSuccess: (res) => {
      setSession(res.token, res.user)
      onLoggedIn()
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    mutation.mutate({ login: loginName, password })
  }

  function fill(name: string) {
    setLoginName(name)
    setPassword(TEST_PASSWORD)
  }

  return (
    <div className="login-page">
      <LoginHero />
      <div className="login-panel">
        <div className="login-mobile-logo">
          <Logo size={40} />
        </div>
        <Card className="login-card">
          <div className="login-head">
            <h1>Вход в AI-buddy</h1>
            <p className="hint">Помощник стажёра. Тестовая среда: все данные синтетические.</p>
          </div>
          <form className="login-form" onSubmit={submit}>
            <Field label="Логин" value={loginName} onChange={(e) => setLoginName(e.target.value)} autoComplete="username" />
            <Field
              label="Пароль"
              type={showPassword ? 'text' : 'password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              suffix={
                <button
                  type="button"
                  className="ui-input-toggle"
                  aria-label={showPassword ? 'Скрыть пароль' : 'Показать пароль'}
                  onClick={() => setShowPassword((v) => !v)}
                >
                  {showPassword ? <EyeOffIcon /> : <EyeIcon />}
                </button>
              }
            />
            {mutation.isError && (
              <p role="alert" className="login-error">
                {loginErrorText(mutation.error)}
              </p>
            )}
            <Button
              type="submit"
              fullWidth
              loading={mutation.isPending}
              disabled={loginName.trim() === '' || password === ''}
            >
              {mutation.isPending ? 'Входим…' : 'Войти'}
            </Button>
          </form>
          <h2 className="accounts-title">Тестовые учётные записи</h2>
          {TEST_ACCOUNT_GROUPS.map((g) => (
            <section key={g.title} className="accounts-group" aria-label={g.title}>
              <h3 className="accounts-group-title">{g.title}</h3>
              <ul className="accounts">
                {g.accounts.map((a) => (
                  <li key={a.login} className={`account${loginName === a.login ? ' account-selected' : ''}`}>
                    <button
                      type="button"
                      className="account-card"
                      aria-label={a.login}
                      aria-pressed={loginName === a.login}
                      onClick={() => fill(a.login)}
                    >
                      <Avatar name={a.login} size="sm" />
                      <span className="account-text">
                        <span className="account-login">{a.login}</span>
                        <span className="account-role">{a.label}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </Card>
      </div>
    </div>
  )
}
