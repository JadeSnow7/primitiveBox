import { Badge } from '@/components/shared/Badge'
import { useTraceStore } from '@/store/traceStore'
import type { TraceEvent as TEvent } from '@/types/trace'
import { cn } from '@/lib/utils'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false })
}

export function TraceEventRow({ event }: { event: TEvent }) {
  const selectedId = useTraceStore((s) => s.selectedId)
  const setSelected = useTraceStore((s) => s.setSelected)
  const isSelected = selectedId === event.id

  const mainVariant = (() => {
    if (event.cvr_depth_exceeded) return 'escalate'
    if (event.recovery_path === 'rollback') return 'rollback'
    if (event.recovery_path === 'retry') return 'retry'
    if (event.recovery_path === 'escalate') return 'escalate'
    if (event.strategy_outcome === 'failed') return 'failed'
    if (event.strategy_outcome === 'passed') return 'passed'
    if (!event.strategy_name) return 'passed'
    return 'neutral'
  })()

  const mainLabel = (() => {
    if (event.cvr_depth_exceeded) return 'depth exceeded'
    if (event.recovery_path) return event.recovery_path
    if (event.strategy_outcome === 'passed') return 'passed'
    if (event.strategy_outcome === 'failed') return 'failed'
    return 'ok'
  })()

  return (
    <div
      className={cn(
        'flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 transition-colors duration-[120ms]',
        isSelected ? 'border-[var(--blue)] bg-[var(--blue-bg)]' : 'border-[var(--border)] hover:bg-[var(--bg-subtle)]'
      )}
      onClick={() => setSelected(isSelected ? null : event.id)}
    >
      <div className="flex flex-shrink-0 flex-col items-center gap-1 pt-1">
        <div
          className={cn(
            'h-2 w-2 rounded-full',
            mainVariant === 'passed'
              ? 'bg-[var(--green)]'
              : mainVariant === 'rollback' || mainVariant === 'failed'
                ? 'bg-[var(--red)]'
                : mainVariant === 'retry' || mainVariant === 'escalate'
                  ? 'bg-[var(--amber)]'
                  : 'bg-[var(--text-muted)]'
          )}
        />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-[12px] font-medium text-[var(--text-mono)]">{event.primitive_id}</span>
          <Badge variant={mainVariant}>{mainLabel}</Badge>
          {event.layer_a_outcome === 'checkpoint_created' ? <Badge variant="checkpoint">checkpoint</Badge> : null}
          <span className="ml-auto flex-shrink-0 font-mono text-[11px] text-[var(--text-muted)]">{formatTime(event.timestamp)}</span>
        </div>

        <div className="mt-1 flex flex-wrap items-center gap-2">
          {event.checkpoint_id ? <span className="font-mono text-[11px] text-[var(--blue)]">{event.checkpoint_id}</span> : null}
          {event.strategy_name ? <span className="text-[11px] text-[var(--text-muted)]">{event.strategy_name}</span> : null}
          {event.attempt > 1 ? <span className="text-[11px] text-[var(--amber)]">attempt {event.attempt}</span> : null}
          <span className="ml-auto text-[11px] text-[var(--text-muted)]">{formatDuration(event.duration_ms)}</span>
        </div>
      </div>
    </div>
  )
}
