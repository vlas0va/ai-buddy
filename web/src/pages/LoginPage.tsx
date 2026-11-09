import { useMutation } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { login } from '@/api/auth'
import { errorMessage } from '@/api/client'
import { useSession } from '@/store/session'
import { TEST_ACCOUNTS, TEST_PASSWORD } from './accounts'

export function LoginPage({ onLoggedIn }: { onLoggedIn: () => void }) {
  const setSession = useSession((s) => s.setSession)
  const [loginName, setLoginName] = useState('')
  const [password, setPassword] = useState('')

  const mutation = useMutation({
    mutationFn: () => login(loginName.trim(), password),
    onSuccess: (res) => {
      setSession(res.token, res.user)
      onLoggedIn()
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    mutation.mutate()
  }

  return (
    <div className="login">
      <h1>Вход в AI-buddy</h1>
      <p className="hint">Тестовая среда: все данные синтетические.</p>
      <form onSubmit={submit}>
        <label>
          Логин
          <input value={loginName} onChange={(e) => setLoginName(e.target.value)} autoComplete="username" />
        </label>
        <label>
          Пароль
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>
        {mutation.isError && (
          <p role="alert" className="state-error">
            {errorMessage(mutation.error)}
          </p>
        )}
        <button type="submit" disabled={mutation.isPending || loginName.trim() === '' || password === ''}>
          {mutation.isPending ? 'Входим…' : 'Войти'}
        </button>
      </form>
      <h2>Тестовые учётные записи</h2>
      <ul className="accounts">
        {TEST_ACCOUNTS.map((a) => (
          <li key={a.login}>
            <button
              type="button"
              onClick={() => {
                setLoginName(a.login)
                setPassword(TEST_PASSWORD)
              }}
            >
              {a.login}
            </button>{' '}
            <span>{a.label}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
