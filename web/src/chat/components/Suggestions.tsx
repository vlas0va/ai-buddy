import { EmptyState } from '@/components/ui/EmptyState'

const SUGGESTIONS = ['Хочу взять отгул в пятницу', 'Какие доступы мне нужны?', 'Кто мой наставник?']

export function Suggestions({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="chat-empty">
      <EmptyState>Спросите buddy о команде, доступах или оформите отгул.</EmptyState>
      <div className="chat-suggestions">
        {SUGGESTIONS.map((s) => (
          <button key={s} type="button" className="chat-chip chat-chip-btn" onClick={() => onPick(s)}>
            {s}
          </button>
        ))}
      </div>
    </div>
  )
}
