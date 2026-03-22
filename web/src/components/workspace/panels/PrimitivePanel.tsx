import type { WorkspacePanel } from '@/types/workspace'

const MOCK_PRIMITIVES = [
  { method: 'fs.read', status: 'completed', duration_ms: 12 },
  { method: 'shell.exec', status: 'completed', duration_ms: 248 },
  { method: 'verify.test', status: 'running', duration_ms: null },
]

const STATUS_STYLE: Record<string, string> = {
  completed: 'text-[var(--green)]',
  running: 'text-[var(--amber)] animate-pulse',
  error: 'text-[var(--red)]',
}

export function PrimitivePanel({ panel: _panel }: { panel: WorkspacePanel }) {
  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
        Primitives
      </div>
      <div className="flex-1 space-y-1.5 overflow-y-auto">
        {MOCK_PRIMITIVES.map((p, i) => (
          <div
            key={i}
            className="flex items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-1.5"
          >
            <span className="font-mono text-[12px] text-[var(--text-mono)]">{p.method}</span>
            <div className="flex items-center gap-3">
              {p.duration_ms !== null && (
                <span className="tabular-nums text-[10px] text-[var(--text-muted)]">
                  {p.duration_ms}ms
                </span>
              )}
              <span className={`text-[11px] font-medium ${STATUS_STYLE[p.status] ?? ''}`}>
                {p.status}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
