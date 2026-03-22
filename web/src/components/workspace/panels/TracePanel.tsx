import type { WorkspacePanel } from '@/types/workspace'

export function TracePanel({ panel }: { panel: WorkspacePanel }) {
  const traceId = (panel.props.trace_id as string | undefined) ?? 'tr-?'
  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="flex items-center gap-2">
        <span className="rounded-full bg-[var(--blue-bg)] px-2 py-0.5 font-mono text-[11px] text-[var(--blue)]">
          {traceId}
        </span>
        <span className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
          Trace
        </span>
      </div>
      <div className="flex-1 space-y-1.5 overflow-y-auto">
        {['started', 'fs.read', 'shell.exec', 'verify.test', 'completed'].map((step, i) => (
          <div
            key={i}
            className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-1.5"
          >
            <span className="text-[10px] tabular-nums text-[var(--text-muted)]">
              +{i * 42}ms
            </span>
            <span className="font-mono text-[12px] text-[var(--text-mono)]">{step}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
