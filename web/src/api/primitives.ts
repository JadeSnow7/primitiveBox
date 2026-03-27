import { apiFetch } from '@/api/client'
import type { PrimitiveSchema } from '@/types/primitive'

interface PrimitiveResponse {
  primitives: Array<{
    name: string
    description: string
    namespace?: string
    input_schema?: object
    input?: object
    output_schema?: object
    output?: object
    ui_layout_hint?: string
    source?: string
    side_effect?: string
    timeout_ms?: number
    scope?: string
    adapter?: string
    status?: string
    intent?: {
      category: string
      side_effect: string
      reversible: boolean
      risk_level: 'low' | 'medium' | 'high'
    }
  }>
}

interface JSONRPCError {
  code: number
  message: string
  data?: unknown
}

interface JSONRPCResponse<T> {
  jsonrpc: '2.0'
  result?: T
  error?: JSONRPCError
  id: string | number | null
}

export async function listPrimitives(): Promise<PrimitiveSchema[]> {
  const data = await apiFetch<PrimitiveResponse>('/api/v1/primitives')
  return data.primitives.map((item) => ({
    name: item.name,
    description: item.description,
    kind: item.source === 'app' ? 'app' : 'system',
    input_schema: item.input_schema ?? item.input ?? {},
    output_schema: item.output_schema ?? item.output ?? {},
    ui_layout_hint: item.ui_layout_hint,
    namespace: item.namespace,
    side_effect: item.side_effect,
    timeout_ms: item.timeout_ms,
    scope: item.scope,
    adapter: item.adapter,
    status: item.status,
    intent: item.intent ?? {
      category: 'mutation',
      side_effect: item.side_effect ?? 'unknown',
      reversible: false,
      risk_level: 'high',
    },
  }))
}

export async function callPrimitive<T>(sandboxId: string, method: string, params: object): Promise<T> {
  const data = await apiFetch<JSONRPCResponse<T | { data: T }>>(`/sandboxes/${sandboxId}/rpc`, {
    method: 'POST',
    body: JSON.stringify({
      jsonrpc: '2.0',
      method,
      params,
      id: `${method}-${Date.now()}`
    })
  })

  if (data.error) {
    throw new Error(data.error.message)
  }

  const result = data.result as T | { data: T } | undefined
  if (result && typeof result === 'object' && 'data' in result) {
    return result.data
  }
  return result as T
}
