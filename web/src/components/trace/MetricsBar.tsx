import { Badge } from '@/components/shared/Badge'
import { useTraceStore } from '@/store/traceStore'

export function MetricsBar() {
  const getMetrics = useTraceStore((s) => s.getMetrics)
  const metrics = getMetrics()

  const items = [
    { label: 'executions', value: metrics.total, variant: 'neutral' as const },
    { label: 'checkpoints', value: metrics.checkpoints, variant: 'checkpoint' as const },
    { label: 'rollbacks', value: metrics.rollbacks, variant: 'rollback' as const },
    { label: 'failures', value: metrics.failures, variant: 'failed' as const }
  ]

  return (
    <div className="grid grid-cols-2 gap-2 border-b border-[var(--border)] p-3 xl:grid-cols-4">
      {items.map((item) => (
        <div key={item.label} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2">
          <div className="flex items-center justify-between">
            <span className="text-[11px] uppercase tracking-[0.16em] text-[var(--text-muted)]">{item.label}</span>
            <Badge variant={item.variant}>{item.value}</Badge>
          </div>
        </div>
      ))}
    </div>
  )
}
