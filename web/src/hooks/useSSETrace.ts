import { useEffect, useRef } from 'react'
import { createSSEConnection } from '@/api/events'
import { useTraceStore } from '@/store/traceStore'
import type { TraceEvent } from '@/types/trace'

function isTraceEvent(value: unknown): value is TraceEvent {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return typeof candidate.trace_id === 'string' && typeof candidate.primitive_id === 'string'
}

export function useSSETrace(sandboxId: string | null) {
  const addEvent = useTraceStore((s) => s.addEvent)
  const setBlocked = useTraceStore((s) => s.setBlocked)
  const setReady = useTraceStore((s) => s.setReady)
  const cleanupRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    if (!sandboxId) return

    cleanupRef.current = createSSEConnection(
      sandboxId,
      (payload) => {
        if (isTraceEvent(payload)) {
          addEvent(payload)
          return
        }
        setBlocked('已连接 SSE，但收到的不是符合 TraceEvent 合约的 CVR trace 数据。')
      },
      () => {
        setReady()
      },
      () => {
        setBlocked('Trace SSE 连接失败，当前无法订阅新的 trace 事件。')
      }
    )

    return () => {
      cleanupRef.current?.()
    }
  }, [addEvent, sandboxId, setBlocked, setReady])
}
