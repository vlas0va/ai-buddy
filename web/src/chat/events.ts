import { z } from 'zod'
import { contactRoleSchema, sourceSchema } from '@/api/schemas'

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
  kind: z.enum(['absence_request', 'access_request']),
  draft: draftSchema,
  summary: z.string().optional(),
})
export type Proposal = z.infer<typeof proposalSchema>

export const resultSchema = z.object({
  action_id: z.string(),
  kind: z.enum(['absence_request', 'access_request']),
  external_id: z.string().nullable().optional(),
  status: z.enum(['pending_approval', 'approved', 'rejected', 'granted', 'created', 'failed']),
  summary: z.string().optional(),
  approver_display_name: z.string().nullable().optional(),
  task_id: z.string().nullable().optional(),
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
