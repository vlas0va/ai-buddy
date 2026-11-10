export function Spinner({ label = 'Загрузка' }: { label?: string }) {
  return <span className="ui-spinner" role="status" aria-label={label} />
}
