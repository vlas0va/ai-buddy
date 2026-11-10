import { initials, tint } from './initials'

export function Avatar({ name, size = 'md' }: { name: string; size?: 'sm' | 'md' }) {
  return (
    <span className={`ui-avatar ui-avatar-${size} ui-avatar-t${tint(name)}`} aria-hidden="true">
      {initials(name)}
    </span>
  )
}
