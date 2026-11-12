import { z } from 'zod'
import { ACTION_KINDS, ACTION_STATUSES, contactRoleSchema, sourceSchema } from '@/api/schemas'

// Event payloads from contracts/agent-stream.md. Every payload is parsed; a bad one becomes a local error event.

export const stageSchema = z.enum(['thinking', 'searching_kb', 'calling_service', 'saving'])
export type Stage = z.infer<typeof stageSchema>

export const draftSchema = z.object({
  tool: z.string(),
  args: z.record(z.string(), z.unknown()),
  display: z.record(z.string(), z.unknown()).optional(),
})
export type Draft = z.infer<typeof draftSchema>

export const proposalSchema = z.object({
  proposal_id: z.string(),
  kind: z.enum([...ACTION_KINDS, 'approval_decision', 'absence_reschedule', 'absence_cancel']),
  draft: draftSchema,
  summary: z.string().optional(),
})
export type Proposal = z.infer<typeof proposalSchema>

export const resultSchema = z.object({
  action_id: z.string(),
  kind: z.enum([...ACTION_KINDS, 'approval_decision', 'absence_reschedule', 'absence_cancel']),
  external_id: z.string().nullable().optional(),
  status: z.enum(ACTION_STATUSES),
  summary: z.string().optional(),
  approver_display_name: z.string().nullable().optional(),
  task_id: z.string().nullable().optional(),
  proposal_id: z.string().optional(),
})
export type ActionResult = z.infer<typeof resultSchema>

export const escalationDraftSchema = z.object({ to_role: contactRoleSchema, text: z.string() })
export type EscalationDraftData = z.infer<typeof escalationDraftSchema>

export const refusalSchema = z.object({
  reason: z.enum(['no_rights', 'out_of_scope']),
  redirect_role: z.enum(['hrbp', 'lead', 'mentor', 'curator']).nullable().optional(),
})
export type Refusal = z.infer<typeof refusalSchema>

export const nextStepSchema = z.object({
  task_id: z.string().optional(),
  checklist_item_id: z.string().nullable().optional(),
  title: z.string(),
  period: z.string().nullable().optional(),
  status: z.string().optional(),
  human_only: z.boolean().optional(),
  cta: z.object({ kind: z.string(), label: z.string(), message: z.string() }).optional(),
})
export type NextStep = z.infer<typeof nextStepSchema>

/** feedback 12.11: a calendar under an absence answer; the chosen dates go back as a message */
export const datePickerSchema = z.object({
  purpose: z.enum(['absence', 'reschedule']),
  kind: z.string(),
  ref_id: z.string().optional(),
  min: z.string(),
  max: z.string(),
  label: z.string(),
  /** feedback 12.13: the user's open and approved requests, marked in the calendar */
  busy: z.array(z.object({ start: z.string(), end: z.string(), label: z.string() })).optional(),
})
export type DatePicker = z.infer<typeof datePickerSchema>

export const streamErrorSchema = z.object({
  code: z.string(),
  message: z.string(),
  retryable: z.boolean(),
})
export type StreamError = z.infer<typeof streamErrorSchema>

export type StreamEvent =
  | { type: 'status'; data: { stage: Stage } }
  | { type: 'token'; data: { text: string } }
  | { type: 'sources'; data: { items: z.infer<typeof sourceSchema>[] } }
  | { type: 'action_proposal'; data: Proposal }
  | { type: 'action_result'; data: ActionResult }
  | { type: 'escalation_draft'; data: EscalationDraftData }
  | { type: 'refusal'; data: Refusal }
  | { type: 'error'; data: StreamError }
  | { type: 'next_step'; data: NextStep }
  | { type: 'date_picker'; data: DatePicker }
  | { type: 'quiz_passed'; data: { checklist_item_id: string } }
  | { type: 'done'; data: { message_id?: string } }
  /** an event this client does not know: contracts/agent-stream.md says new events are additive and must be skipped */
  | { type: 'ignored'; data: { name: string } }

const payloads = {
  status: z.object({ stage: stageSchema }),
  token: z.object({ text: z.string() }),
  sources: z.object({ items: z.array(sourceSchema) }),
  action_proposal: proposalSchema,
  action_result: resultSchema,
  escalation_draft: escalationDraftSchema,
  refusal: refusalSchema,
  error: streamErrorSchema,
  next_step: nextStepSchema,
  date_picker: datePickerSchema,
  quiz_passed: z.object({ checklist_item_id: z.string() }),
  done: z.object({ message_id: z.string().optional() }),
} as const

export const BAD_EVENT: StreamEvent = {
  type: 'error',
  data: {
    code: 'internal',
    message: 'Не удалось разобрать ответ сервера. Повторите попытку.',
    retryable: true,
  },
}

export function parseEvent(name: string, raw: string): StreamEvent {
  if (!(name in payloads)) return { type: 'ignored', data: { name } }
  let json: unknown
  try {
    json = JSON.parse(raw)
  } catch {
    return BAD_EVENT
  }
  const parsed = payloads[name as keyof typeof payloads].safeParse(json)
  if (!parsed.success) return BAD_EVENT
  return { type: name, data: parsed.data } as StreamEvent
}
