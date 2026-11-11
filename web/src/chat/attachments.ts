import type { AttachmentMeta } from '@/api/schemas'

// Files in chat (contracts/openapi.yaml MessageAttachment): text files (logs, configs, code) are read in the browser and
// sent as text; screenshots and documents are sent as metadata only — their content never leaves the browser.

export interface OutgoingAttachment extends AttachmentMeta {
  text?: string
}

export const MAX_ATTACHMENTS = 3
export const MAX_TEXT_BYTES = 32 * 1024
export const MAX_FILE_BYTES = 20 * 1024 * 1024
export const ACCEPT =
  '.log,.txt,.out,.json,.yaml,.yml,.toml,.ini,.env,.conf,.cfg,.properties,.py,.go,.ts,.tsx,.js,.jsx,.java,.kt,.sql,.sh,' +
  '.md,.csv,.xml,image/*,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx'

const TEXT_EXT = /\.(log|txt|out|err|json|ya?ml|toml|ini|env|conf|cfg|properties|py|go|tsx?|jsx?|java|kt|sql|sh|md|csv|xml)$/i
const DOC_EXT = /\.(pdf|docx?|xlsx?|pptx?|odt|rtf)$/i

export function attachmentKind(file: Pick<File, 'name' | 'type'>): AttachmentMeta['kind'] | null {
  if (file.type.startsWith('image/')) return 'image'
  if (DOC_EXT.test(file.name) || file.type === 'application/pdf') return 'document'
  if (file.type.startsWith('text/') || TEXT_EXT.test(file.name) || /^\.env/i.test(file.name) || /^dockerfile$/i.test(file.name)) {
    return 'text'
  }
  return null
}

/** Reads one file into an attachment or returns a user-facing reason why it cannot be attached. */
export async function readAttachment(file: File): Promise<OutgoingAttachment | string> {
  const kind = attachmentKind(file)
  if (!kind) return `«${file.name}»: такой тип файла не поддерживается. Можно приложить лог, конфиг, код, скриншот или документ.`
  if (file.size > MAX_FILE_BYTES) return `«${file.name}»: файл больше 20 МБ.`
  const meta: OutgoingAttachment = { name: file.name, kind, mime: file.type || undefined, size: file.size }
  if (kind !== 'text') return meta
  if (file.size > MAX_TEXT_BYTES) return `«${file.name}»: текстовый файл больше 32 КБ — пришлите нужный фрагмент.`
  return { ...meta, text: await file.text() }
}

export const KIND_TEXT: Record<AttachmentMeta['kind'], string> = { text: 'файл', image: 'скриншот', document: 'документ' }

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} Б`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} КБ`
  return `${(bytes / 1024 / 1024).toFixed(1)} МБ`
}

/** Only http(s) links of external sources are rendered as links. */
export function safeUrl(url: string | undefined): string | undefined {
  return url && /^https?:\/\//i.test(url) ? url : undefined
}
