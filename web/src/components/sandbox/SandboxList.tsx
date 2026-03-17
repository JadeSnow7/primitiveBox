import { Badge } from '@/components/shared/Badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useSandbox } from '@/hooks/useSandbox'
import { SandboxCard } from '@/components/sandbox/SandboxCard'

export function SandboxList() {
  const { sandboxes, selectedId, select, destroy, loading, error, capabilityNotice } = useSandbox()

  return (
    <Card className="flex h-full min-h-[70vh] flex-col overflow-hidden">
      <CardHeader>
        <div>
          <CardTitle>Sandbox Fleet</CardTitle>
          <div className="mt-1 text-[11px] uppercase tracking-[0.2em] text-[var(--text-muted)]">Control-plane records</div>
        </div>
        <Badge variant="neutral">{sandboxes.length}</Badge>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col gap-3">
        {error ? <div className="rounded-lg border border-[var(--red)]/20 bg-[var(--red-bg)] p-3 text-[12px] text-[var(--red)]">{error}</div> : null}
        {capabilityNotice ? (
          <div className="rounded-lg border border-[var(--amber)]/25 bg-[var(--amber-bg)] p-3 text-[12px] text-[var(--amber)]">{capabilityNotice}</div>
        ) : null}
        <ScrollArea className="min-h-0 flex-1">
          <div className="space-y-2 pr-1">
            {sandboxes.map((sandbox) => (
              <div key={sandbox.id} className="space-y-2">
                <SandboxCard sandbox={sandbox} selected={selectedId === sandbox.id} onSelect={() => select(sandbox.id)} />
                {selectedId === sandbox.id ? (
                  <div className="flex gap-2">
                    <Button variant="ghost" size="sm" className="flex-1" onClick={() => void destroy(sandbox.id)}>
                      Destroy
                    </Button>
                  </div>
                ) : null}
              </div>
            ))}
            {!loading && sandboxes.length === 0 ? (
              <div className="rounded-xl border border-dashed border-[var(--border-strong)] p-5 text-center text-[12px] text-[var(--text-muted)]">
                No sandboxes available from `GET /api/v1/sandboxes`.
              </div>
            ) : null}
          </div>
        </ScrollArea>
      </CardContent>
    </Card>
  )
}
