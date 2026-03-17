import { apiFetch } from '@/api/client'
import type { TraceEvent } from '@/types/trace'

interface TraceListResponse {
  events: TraceEvent[]
}

export async function listTraceEvents(sandboxId: string, limit = 100): Promise<TraceEvent[]> {
  const data = await apiFetch<TraceListResponse>(`/api/v1/sandboxes/${sandboxId}/trace?limit=${limit}`)
  return data.events
}

export async function getTraceEvent(sandboxId: string, stepId: string): Promise<TraceEvent> {
  return apiFetch<TraceEvent>(`/api/v1/sandboxes/${sandboxId}/trace/${stepId}`)
}
