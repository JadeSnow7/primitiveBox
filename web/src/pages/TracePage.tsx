import { useEffect } from 'react'
import { listTraceEvents } from '@/api/traces'
import { useSSETrace } from '@/hooks/useSSETrace'
import { useSandboxStore } from '@/store/sandboxStore'
import { useTraceStore } from '@/store/traceStore'
import { DetailPanel } from '@/components/trace/DetailPanel'
import { MetricsBar } from '@/components/trace/MetricsBar'
import { TracePanel } from '@/components/trace/TracePanel'

export function TracePage() {
  const selectedSandboxId = useSandboxStore((s) => s.selectedId)
  const clearEvents = useTraceStore((s) => s.clearEvents)
  const setEvents = useTraceStore((s) => s.setEvents)
  const setBlocked = useTraceStore((s) => s.setBlocked)
  const setReady = useTraceStore((s) => s.setReady)
  const streamState = useTraceStore((s) => s.streamState)
  const blockReason = useTraceStore((s) => s.blockReason)

  useEffect(() => {
    clearEvents()
    if (!selectedSandboxId) return

    let active = true
    void listTraceEvents(selectedSandboxId)
      .then((events) => {
        if (!active) return
        setEvents(events)
        setReady()
      })
      .catch((error) => {
        if (!active) return
        setBlocked(error instanceof Error ? error.message : 'Failed to load trace history')
      })
    return () => {
      active = false
    }
  }, [clearEvents, selectedSandboxId, setBlocked, setEvents, setReady])

  useSSETrace(selectedSandboxId)

  if (!selectedSandboxId) {
    return <div className="flex h-full items-center justify-center text-sm text-[var(--text-muted)]">从左侧选择一个 sandbox</div>
  }

  if (streamState === 'blocked') {
    return (
      <div className="flex h-full flex-col">
        <MetricsBar />
        <div className="flex flex-1 items-center justify-center p-6">
          <div className="max-w-2xl rounded-2xl border border-[var(--amber)]/25 bg-[var(--amber-bg)] p-6">
            <div className="text-[11px] uppercase tracking-[0.22em] text-[var(--amber)]">Trace Blocked</div>
            <div className="mt-3 text-lg font-medium">当前后端尚未提供可直接消费的 CVR trace 数据面</div>
            <p className="mt-3 text-[13px] leading-6 text-[var(--text-secondary)]">
              {blockReason ??
                'TracePage 已按 TraceEvent 合约就绪，但 PrimitiveBox 目前只有通用 control-plane 事件流。为避免把普通事件错误映射成 CVR 语义，页面保持显式阻塞态。'}
            </p>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full overflow-hidden">
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <MetricsBar />
        <div className="flex-1 overflow-y-auto p-3">
          <TracePanel />
        </div>
      </div>
      <div className="hidden w-[300px] flex-shrink-0 border-l border-[var(--border)] xl:block">
        <DetailPanel />
      </div>
    </div>
  )
}
