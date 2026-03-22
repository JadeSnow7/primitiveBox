import { useEffect, useState } from 'react'
import type { WorkspacePanel } from '@/types/workspace'

interface StreamEvent {
  id: string
  type: string
  data: string
}

export function EventStreamPanel({ panel: _panel }: { panel: WorkspacePanel }) {
  const [events, setEvents] = useState<StreamEvent[]>([])

  // Simulate incoming events for demo (replace with real SSE subscription)
  useEffect(() => {
    const MOCK = [
      { type: 'rpc.started', data: 'shell.exec' },
      { type: 'stdout', data: 'Running test suite...' },
      { type: 'stdout', data: 'PASS  src/utils.test.ts' },
      { type: 'rpc.completed', data: 'shell.exec → 0' },
    ]
    let i = 0
    const id = setInterval(() => {
      if (i >= MOCK.length) {
        clearInterval(id)
        return
      }
      setEvents((prev) => [
        ...prev,
        { id: `ev-${i}`, type: MOCK[i].type, data: MOCK[i].data },
      ])
      i++
    }, 600)
    return () => clearInterval(id)
  }, [])

  return (
    <div className="flex h-full flex-col gap-2 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
        Event Stream
      </div>
      <div className="flex-1 space-y-1 overflow-y-auto">
        {events.map((ev) => (
          <div key={ev.id} className="flex gap-2 rounded px-2 py-1 hover:bg-[var(--bg-subtle)]">
            <span className="w-28 shrink-0 font-mono text-[10px] text-[var(--blue)]">{ev.type}</span>
            <span className="font-mono text-[11px] text-[var(--text-secondary)]">{ev.data}</span>
          </div>
        ))}
        {events.length === 0 && (
          <div className="text-[12px] text-[var(--text-muted)]">Waiting for events…</div>
        )}
      </div>
    </div>
  )
}
