import type { WorkspacePanel } from '@/types/workspace'

const MOCK_SANDBOXES = [
  { id: 'sb-abc123', status: 'running', runtime: 'docker', image: 'ubuntu:22.04' },
  { id: 'sb-def456', status: 'stopped', runtime: 'docker', image: 'node:20' },
]

const STATUS_COLORS: Record<string, string> = {
  running: 'var(--green)',
  stopped: 'var(--text-muted)',
  error: 'var(--red)',
}

export function SandboxPanel({ panel }: { panel: WorkspacePanel }) {
  const sandboxId = panel.props.sandbox_id as string | undefined
  const sandboxes = sandboxId
    ? MOCK_SANDBOXES.filter((s) => s.id === sandboxId)
    : MOCK_SANDBOXES

  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
        Sandboxes
      </div>
      <div className="space-y-2">
        {sandboxes.map((sb) => (
          <div
            key={sb.id}
            className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-3"
          >
            <div className="flex items-center gap-2">
              <span
                className="h-2 w-2 rounded-full"
                style={{ background: STATUS_COLORS[sb.status] ?? 'var(--text-muted)' }}
              />
              <span className="font-mono text-[12px] text-[var(--text-mono)]">{sb.id}</span>
            </div>
            <div className="mt-1.5 flex gap-3 text-[11px] text-[var(--text-secondary)]">
              <span>{sb.runtime}</span>
              <span>{sb.image}</span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
