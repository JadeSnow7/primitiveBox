import { Badge } from '@/components/shared/Badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type View = 'trace' | 'primitives' | 'app-primitives' | 'workspace'

const items: Array<{ id: View; label: string; short: string; description: string }> = [
  { id: 'trace', label: 'CVR Trace', short: 'T', description: 'Recovery and checkpoint instrumentation' },
  { id: 'primitives', label: 'System Primitives', short: 'P', description: 'Schema-driven primitive execution' },
  { id: 'app-primitives', label: 'App Primitives', short: 'A', description: 'Registered application-level contracts' },
  { id: 'workspace', label: 'AI Workspace', short: 'W', description: 'AI-controlled panel workspace via UI primitives' },
]

export function Sidebar({
  view,
  onViewChange
}: {
  view: View
  onViewChange: (view: View) => void
}) {
  return (
    <aside className="panel-surface flex h-full w-[228px] flex-col p-3">
      <div className="mb-5 flex items-center justify-between px-1">
        <div>
          <div className="text-[11px] font-medium uppercase tracking-[0.24em] text-[var(--text-muted)]">PrimitiveBox</div>
          <div className="mt-1 text-sm font-medium">Developer Debug UI</div>
        </div>
        <Badge variant="neutral">v0</Badge>
      </div>

      <div className="space-y-2">
        {items.map((item) => (
          <Button
            key={item.id}
            variant="ghost"
            className={cn(
              'h-auto w-full justify-start rounded-xl border px-3 py-3 text-left',
              view === item.id
                ? 'border-[var(--blue)] bg-[var(--blue-bg)] text-[var(--text-primary)]'
                : 'border-[var(--border)] bg-transparent hover:bg-[var(--bg-subtle)]'
            )}
            onClick={() => onViewChange(item.id)}
          >
            <div className="flex items-start gap-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md border border-[var(--border)] font-mono text-[12px] text-[var(--text-mono)]">
                {item.short}
              </div>
              <div>
                <div className="text-[13px] font-medium">{item.label}</div>
                <div className="mt-1 text-[11px] leading-5 text-[var(--text-muted)]">{item.description}</div>
              </div>
            </div>
          </Button>
        ))}
      </div>
    </aside>
  )
}
