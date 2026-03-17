import { Badge } from '@/components/shared/Badge'
import { MonoText } from '@/components/shared/MonoText'
import { Button } from '@/components/ui/button'
import { useSandbox } from '@/hooks/useSandbox'
import { useUIStore } from '@/store/uiStore'

function connectionVariant(status: 'checking' | 'online' | 'offline') {
  if (status === 'online') return 'running'
  if (status === 'offline') return 'failed'
  return 'neutral'
}

export function Topbar() {
  const { selectedSandbox } = useSandbox()
  const gatewayStatus = useUIStore((s) => s.gatewayStatus)
  const setCreateDialogOpen = useUIStore((s) => s.setCreateDialogOpen)

  return (
    <header className="panel-surface flex items-center justify-between px-4 py-3">
      <div className="flex items-center gap-4">
        <div>
          <div className="text-[11px] uppercase tracking-[0.24em] text-[var(--text-muted)]">Active Sandbox</div>
          <div className="mt-1 flex items-center gap-2">
            <MonoText>{selectedSandbox?.id ?? 'none selected'}</MonoText>
            {selectedSandbox ? <Badge variant={selectedSandbox.status === 'running' ? 'running' : 'stopped'}>{selectedSandbox.status}</Badge> : null}
          </div>
        </div>
        <div className="h-8 w-px bg-[var(--border)]" />
        <div className="flex items-center gap-3 text-[12px] text-[var(--text-secondary)]">
          <span>driver</span>
          <MonoText>{selectedSandbox?.driver ?? '--'}</MonoText>
          <span>ttl</span>
          <MonoText>{selectedSandbox ? `${selectedSandbox.ttl_seconds}s` : '--'}</MonoText>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Badge variant={connectionVariant(gatewayStatus)}>{gatewayStatus}</Badge>
        <Button size="sm" onClick={() => setCreateDialogOpen(true)}>
          New Sandbox
        </Button>
      </div>
    </header>
  )
}
