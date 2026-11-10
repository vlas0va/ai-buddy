import { Logo } from '@/components/ui/Logo'
import { ChatIcon, SendIcon, StepsIcon } from '@/components/ui/icons'

const BENEFITS = [
  { icon: <StepsIcon />, text: 'План адаптации по шагам' },
  { icon: <ChatIcon />, text: 'Ответы о команде и инструментах' },
  { icon: <SendIcon />, text: 'Заявки в сервисы прямо из чата' },
]

export function LoginHero() {
  return (
    <section className="login-hero" aria-label="О продукте">
      <div className="login-hero-main">
        <div className="brand">
          <Logo size={40} />
          <span className="brand-name">AI-buddy</span>
        </div>
        <p className="login-hero-title">Помощник для первых недель стажировки</p>
        <ul className="benefits">
          {BENEFITS.map((b) => (
            <li key={b.text} className="benefit">
              <span className="benefit-icon">{b.icon}</span>
              {b.text}
            </li>
          ))}
        </ul>
      </div>
      <p className="hint">Прототип. Все данные синтетические.</p>
    </section>
  )
}
