// Original product mark: two overlapping speech bubbles on an accent rounded square.
export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg
      className="ui-logo"
      width={size}
      height={size}
      viewBox="0 0 32 32"
      aria-hidden="true"
      focusable="false"
    >
      <rect width="32" height="32" rx="8" fill="var(--color-accent)" />
      <path
        d="M8 9.5A2.5 2.5 0 0 1 10.5 7h8A2.5 2.5 0 0 1 21 9.5v4a2.5 2.5 0 0 1-2.5 2.5H14l-3 2.5V16h-.5A2.5 2.5 0 0 1 8 13.5z"
        fill="#fff"
      />
      <path
        d="M16 17.5a2.5 2.5 0 0 1 2.5-2.5H22a2 2 0 0 1 2 2v4.5a2.5 2.5 0 0 1-2.5 2.5H21v2.5L18 24h-.5A2.5 2.5 0 0 1 15 21.5z"
        fill="#fff"
        fillOpacity="0.6"
        stroke="var(--color-accent)"
        strokeWidth="1.5"
      />
    </svg>
  )
}
