import { Link } from '@tanstack/react-router'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { NextStep } from '../events'

/** «Следующий шаг» из события `next_step` (S1): кнопка отправляет предложенное сообщение новым ходом. */
export function NextStepCard({ step, disabled, onAsk }: { step: NextStep; disabled: boolean; onAsk: (text: string) => void }) {
  return (
    <Card className="chat-card next-step" aria-label="Следующий шаг">
      <span className="next-step-label">Следующий шаг</span>
      <span className="next-step-title">{step.title}</span>
      {step.human_only && <p className="hint">Этот шаг делаешь ты сам вместе с командой — buddy поможет подготовиться.</p>}
      <div className="chat-actions">
        {step.cta && (
          <Button size="sm" disabled={disabled} onClick={() => onAsk(step.cta?.message ?? step.title)}>
            {step.cta.label}
          </Button>
        )}
        <Link to="/plan" className="chat-link">
          Открыть план
        </Link>
      </div>
    </Card>
  )
}
