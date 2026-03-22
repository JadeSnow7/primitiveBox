import type { WorkspacePanel } from '@/types/workspace'

const MOCK_CHECKPOINTS = [
  { id: 'ckpt-001', label: 'before refactor', created_at: '2026-03-21T10:00:00Z' },
  { id: 'ckpt-002', label: 'after test fix', created_at: '2026-03-21T11:30:00Z' },
  { id: 'ckpt-003', label: 'green tests', created_at: '2026-03-21T12:45:00Z' },
]

export function CheckpointPanel({ panel: _panel }: { panel: WorkspacePanel }) {
  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
        Checkpoints
      </div>
      <div className="flex-1 space-y-1.5 overflow-y-auto">
        {MOCK_CHECKPOINTS.map((ckpt) => (
          <div
            key={ckpt.id}
            className="flex items-start gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2"
          >
            <div className="mt-0.5 h-2 w-2 shrink-0 rounded-full bg-[var(--teal)]" />
            <div>
              <div className="font-mono text-[12px] text-[var(--text-mono)]">{ckpt.id}</div>
              <div className="mt-0.5 text-[11px] text-[var(--text-secondary)]">{ckpt.label}</div>
              <div className="mt-0.5 text-[10px] text-[var(--text-muted)]">
                {new Date(ckpt.created_at).toLocaleTimeString()}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
