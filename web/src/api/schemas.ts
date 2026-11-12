import { z } from 'zod'

// Mirrors contracts/openapi.yaml. Every API response is parsed with one of these (R63).

export const errorCodeSchema = z.enum([
  'unauthorized',
  'forbidden',
  'not_found',
  'validation_error',
  'conflict',
  'expired',
  'rate_limited',
  'upstream_unavailable',
  'internal',
])

export const errorSchema = z.object({
  code: errorCodeSchema,
  message: z.string(),
  retryable: z.boolean(),
})

export const roleSchema = z.enum(['intern', 'mentor', 'lead', 'hrbp', 'alumni', 'hr_admin'])
export const contactRoleSchema = z.enum(['mentor', 'lead', 'hrbp', 'curator'])
export const periodSchema = z.enum(['week_1', 'week_2', 'month_1', 'month_2', 'after_3_months'])
export const taskStatusSchema = z.enum([
  'not_started',
  'in_progress',
  'waiting_approval',
  'done',
  'overdue',
])

export const userSchema = z.object({
  id: z.string(),
  login: z.string(),
  display_name: z.string(),
  role: roleSchema,
  team_id: z.string(),
  team_name: z.string().optional(),
  cohort_start: z.string().optional(),
})

export const flagsSchema = z.record(z.string(), z.boolean())

export const healthSchema = z.object({ status: z.literal('ok'), service: z.string() })
export const loginResponseSchema = z.object({ token: z.string(), user: userSchema })
export const meResponseSchema = z.object({ user: userSchema, flags: flagsSchema })

export const goalSchema = z.object({
  id: z.string(),
  created_by: z.string(),
  title: z.string(),
  criteria: z.string().optional(),
  status: z.enum(['proposed', 'active', 'achieved', 'missed']).default('active'),
})

export const nextStepSchema = z.object({
  task_id: z.string(),
  title: z.string(),
  action: z.enum(['ask_buddy', 'request_access', 'absence_request', 'draft_message', 'mark_done']),
  prompt: z.string().optional(),
})

export interface Task {
  id: string
  source: 'checklist' | 'action'
  checklist_item_id: string | null
  parent_task_id?: string | null
  period: z.infer<typeof periodSchema>
  title: string
  description?: string | null
  status: z.infer<typeof taskStatusSchema>
  due_at?: string | null
  human_only: boolean
  linked_action_id?: string | null
  /** who confirms it is done (organizers 12.13): self, a mini-check in the chat, or the mentor */
  confirm?: 'self' | 'quiz' | 'mentor'
  subtasks?: Task[]
}

export const taskSchema: z.ZodType<Task> = z.lazy(() =>
  z.object({
    id: z.string(),
    source: z.enum(['checklist', 'action']),
    checklist_item_id: z.string().nullable(),
    parent_task_id: z.string().nullable().optional(),
    period: periodSchema,
    title: z.string(),
    description: z.string().nullable().optional(),
    status: taskStatusSchema,
    due_at: z.string().nullable().optional(),
    human_only: z.boolean(),
    linked_action_id: z.string().nullable().optional(),
    confirm: z.enum(['self', 'quiz', 'mentor']).optional(),
    subtasks: z.array(taskSchema).optional(),
  }),
)

export const planSchema = z.object({
  user_id: z.string(),
  plan_role: z.enum(['intern', 'mentor']),
  intern_id: z.string().nullable().optional(),
  current_period: periodSchema,
  periods: z.array(z.object({ id: periodSchema, label: z.string(), tasks: z.array(taskSchema) })),
  goals: z.array(goalSchema),
  next_step: nextStepSchema.nullable().optional(),
})

export const internProgressSchema = z.object({
  user_id: z.string(),
  display_name: z.string(),
  current_period: periodSchema,
  done: z.number().int(),
  total: z.number().int(),
  overdue: z.number().int(),
  pending_access_requests: z.number().int().optional(),
})

export const sourceSchema = z.object({
  doc_id: z.string(),
  title: z.string(),
  // test = synthetic KB document (R69); external = public page from web search for a general technical question
  source_type: z.enum(['test', 'external']),
  url: z.string().optional(),
})

export const ATTACHMENT_KINDS = ['text', 'image', 'document'] as const
export const attachmentSchema = z.object({
  name: z.string(),
  kind: z.enum(ATTACHMENT_KINDS),
  mime: z.string().optional(),
  size: z.number().int().nonnegative(),
})
export type AttachmentMeta = z.infer<typeof attachmentSchema>

export const chatMessageSchema = z.object({
  id: z.string(),
  author: z.enum(['user', 'assistant']),
  text: z.string(),
  sources: z.array(sourceSchema).optional(),
  attachments: z.array(attachmentSchema).optional(),
  created_at: z.string(),
})

export const chatSessionSchema = z.object({ session_id: z.string() })
export const chatHistorySchema = z.object({
  session_id: z.string(),
  messages: z.array(chatMessageSchema),
})

export const ACTION_KINDS = ['absence_request', 'access_request', 'trip_request', 'consent'] as const
export const ACTION_STATUSES = ['pending_approval', 'pending_hr', 'approved', 'rejected', 'granted', 'created', 'failed', 'signed'] as const

export const actionSchema = z.object({
  id: z.string(),
  kind: z.enum(ACTION_KINDS),
  external_id: z.string().nullable().optional(),
  status: z.enum(ACTION_STATUSES),
  summary: z.string(),
  approver_display_name: z.string().nullable().optional(),
  task_id: z.string().nullable().optional(),
  created_at: z.string(),
  updated_at: z.string().optional(),
})
export const actionsSchema = z.array(actionSchema)

export const approvalSchema = z.object({
  id: z.string(),
  kind: z.enum(['absence_request', 'access_request', 'trip_request']),
  requester_display_name: z.string(),
  summary: z.string(),
  status: z.enum(['pending', 'approved', 'rejected', 'cancelled']),
  comment: z.string().nullable().optional(),
  /** lead first, then the HR admin for an absence request (checkpoint 11.11) */
  stage: z.enum(['lead', 'hr_admin']).optional(),
  created_at: z.string(),
})

export const escalationSchema = z.object({
  id: z.string(),
  from_display_name: z.string().optional(),
  to_role: contactRoleSchema,
  text: z.string(),
  status: z.enum(['open', 'answered']),
  answer: z.string().nullable().optional(),
  added_to_kb: z.boolean().optional(),
  created_at: z.string(),
})

export const notificationSchema = z.object({
  id: z.string(),
  kind: z.enum(['reminder', 'overdue', 'status_change', 'escalation_answer']),
  title: z.string(),
  body: z.string().nullable().optional(),
  checklist_item_id: z.string().nullable().optional(),
  read: z.boolean().optional(),
  created_at: z.string(),
})

export type ApiErrorBody = z.infer<typeof errorSchema>
export type Role = z.infer<typeof roleSchema>
export type User = z.infer<typeof userSchema>
export type Plan = z.infer<typeof planSchema>
export type Action = z.infer<typeof actionSchema>
export type Goal = z.infer<typeof goalSchema>
export type NextStepInfo = z.infer<typeof nextStepSchema>
export type InternProgress = z.infer<typeof internProgressSchema>
export type Period = z.infer<typeof periodSchema>
export type TaskStatus = z.infer<typeof taskStatusSchema>
