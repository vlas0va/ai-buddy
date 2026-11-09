import { useId, type InputHTMLAttributes, type LabelHTMLAttributes, type ReactNode } from 'react'

export function Label({ className, ...rest }: LabelHTMLAttributes<HTMLLabelElement>) {
  return <label {...rest} className={['ui-label', className].filter(Boolean).join(' ')} />
}

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={['ui-input', className].filter(Boolean).join(' ')} />
}

interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: string
  error?: string
  suffix?: ReactNode
}

// Label bound to its input; the error text is linked through aria-describedby.
export function Field({ label, error, suffix, ...rest }: FieldProps) {
  const id = useId()
  const errId = `${id}-err`
  return (
    <div className="ui-field">
      <Label htmlFor={id}>{label}</Label>
      <div className="ui-input-wrap">
        <Input
          {...rest}
          id={id}
          className={suffix ? 'ui-input-has-suffix' : undefined}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errId : undefined}
        />
        {suffix}
      </div>
      {error && (
        <p id={errId} className="ui-field-error">
          {error}
        </p>
      )}
    </div>
  )
}
