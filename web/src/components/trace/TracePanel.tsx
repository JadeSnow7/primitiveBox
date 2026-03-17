import { TraceEventRow } from '@/components/trace/TraceEvent'
import { useTraceStore } from '@/store/traceStore'

export function TracePanel() {
  const events = useTraceStore((s) => s.events)

  if (events.length === 0) {
    return <div className="pt-12 text-center text-sm text-[var(--text-muted)]">等待 CVR 事件...</div>
  }

  return (
    <div className="space-y-1.5">
      {events.map((event) => (
        <TraceEventRow key={event.id} event={event} />
      ))}
    </div>
  )
}
