import type { ButtonHTMLAttributes } from 'react'

type Variant = 'primary' | 'secondary' | 'ghost'
type Size = 'sm' | 'md'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
  fullWidth?: boolean
}

export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  fullWidth = false,
  disabled,
  className,
  children,
  type = 'button',
  ...rest
}: Props) {
  const cls = ['ui-btn', `ui-btn-${variant}`, `ui-btn-${size}`, fullWidth && 'ui-btn-full', className]
    .filter(Boolean)
    .join(' ')
  return (
    <button {...rest} type={type} className={cls} disabled={disabled || loading} aria-busy={loading || undefined}>
      {loading && <span className="ui-btn-spin" aria-hidden="true" />}
      {children}
    </button>
  )
}
