import type { ReactNode } from 'react'
import { EXTERNAL_BADGE_TEXT, TEST_BADGE_TEXT } from './badgeText'

export type BadgeVariant =
  | 'neutral'
  | 'not_started'
  | 'in_progress'
  | 'waiting_approval'
  | 'done'
  | 'overdue'
  | 'test'
  | 'external'

export function Badge({ variant = 'neutral', children }: { variant?: BadgeVariant; children?: ReactNode }) {
  return <span className={`ui-badge ui-badge-${variant}`}>{variant === 'test' ? (children ?? TEST_BADGE_TEXT) : variant === 'external' ? (children ?? EXTERNAL_BADGE_TEXT) : children}</span>
}
