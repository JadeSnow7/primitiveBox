import type { TraceEvent } from '@/types/trace'

export function createSSEConnection(
  sandboxId: string,
  onEvent: (event: TraceEvent) => void,
  onOpen?: () => void,
  onError?: (e: Event) => void
): () => void {
  const es = new EventSource(`/api/v1/sandboxes/${sandboxId}/trace/stream`)
  const handler = (e: MessageEvent) => {
    try {
      onEvent(JSON.parse(e.data))
    } catch {
      // ignore parse errors
    }
  }
  es.onopen = () => {
    onOpen?.()
  }
  es.onmessage = handler
  es.addEventListener('trace.step', handler as EventListener)
  if (onError) es.onerror = onError
  return () => {
    es.removeEventListener('trace.step', handler as EventListener)
    es.close()
  }
}
