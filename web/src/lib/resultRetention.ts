const MAX_STRING_LENGTH = 4000
const MAX_ARRAY_ITEMS = 50
const MAX_OBJECT_KEYS = 50
const MAX_DEPTH = 6

function stripExecutableMarkup(input: string): string {
  return input
    .replace(/<script[\s\S]*?>[\s\S]*?<\/script>/gi, '')
    .replace(/<style[\s\S]*?>[\s\S]*?<\/style>/gi, '')
}

function truncateString(input: string): string {
  const sanitized = stripExecutableMarkup(input)
  if (sanitized.length <= MAX_STRING_LENGTH) {
    return sanitized
  }
  const omitted = sanitized.length - MAX_STRING_LENGTH
  return `${sanitized.slice(0, MAX_STRING_LENGTH)}\n… [truncated ${omitted} chars]`
}

export function retainExecutionPayload(value: unknown, depth = 0): unknown {
  if (value == null || typeof value === 'number' || typeof value === 'boolean') {
    return value
  }

  if (typeof value === 'string') {
    return truncateString(value)
  }

  if (depth >= MAX_DEPTH) {
    return '[truncated: max depth reached]'
  }

  if (Array.isArray(value)) {
    return value.slice(0, MAX_ARRAY_ITEMS).map((item) => retainExecutionPayload(item, depth + 1))
  }

  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
    const retained = entries.slice(0, MAX_OBJECT_KEYS).map(([key, item]) => [key, retainExecutionPayload(item, depth + 1)])
    const objectValue = Object.fromEntries(retained)
    if (entries.length > MAX_OBJECT_KEYS) {
      objectValue.__pb_truncated__ = `omitted ${entries.length - MAX_OBJECT_KEYS} keys`
    }
    return objectValue
  }

  return String(value)
}

export function retainPanelProps(props: Record<string, unknown>): Record<string, unknown> {
  return retainExecutionPayload(props) as Record<string, unknown>
}
