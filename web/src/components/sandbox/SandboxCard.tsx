import { Badge } from '@/components/shared/Badge'
import { MonoText } from '@/components/shared/MonoText'
import { cn } from '@/lib/utils'
import type { Sandbox } from '@/types/sandbox'

function formatTime(iso: string) {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}

export function SandboxCard({
  sandbox,
  selected,
  onSelect
}: {
  sandbox: Sandbox
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      className={cn(
        'w-full rounded-xl border p-3 text-left transition-colors duration-[120ms]',
        selected
          ? 'border-[var(--blue)] bg-[var(--blue-bg)]'
          : 'border-[var(--border)] bg-[var(--bg-surface)] hover:bg-[var(--bg-subtle)]'
      )}
      onClick={onSelect}
    >
      <div className="flex items-center justify-between gap-3">
        <MonoText className="truncate">{sandbox.id}</MonoText>
        <Badge variant={sandbox.status === 'running' ? 'running' : sandbox.status === 'error' ? 'failed' : 'stopped'}>
          {sandbox.status}
        </Badge>
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2 text-[11px] text-[var(--text-muted)]">
        <span>{sandbox.driver}</span>
        <span className="text-right">{sandbox.ttl_seconds}s TTL</span>
        <span className="col-span-2 truncate">{sandbox.workspace_root}</span>
        <span className="col-span-2">{formatTime(sandbox.created_at)}</span>
      </div>
    </button>
  )
}
