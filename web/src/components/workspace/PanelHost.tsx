import { CheckpointPanel } from './panels/CheckpointPanel'
import { DiffPanel } from './panels/DiffPanel'
import { EventStreamPanel } from './panels/EventStreamPanel'
import { PrimitivePanel } from './panels/PrimitivePanel'
import { SandboxPanel } from './panels/SandboxPanel'
import { TracePanel } from './panels/TracePanel'
import { useWorkspaceStore } from '@/store/workspaceStore'
import type { PanelType, WorkspacePanel } from '@/types/workspace'

const PANEL_LABELS: Record<PanelType, string> = {
  trace: 'Trace',
  event_stream: 'Event Stream',
  sandbox: 'Sandbox',
  checkpoint: 'Checkpoint',
  diff: 'Diff',
  primitive: 'Primitives',
}

function PanelContent({ panel }: { panel: WorkspacePanel }) {
  switch (panel.type) {
    case 'trace':        return <TracePanel panel={panel} />
    case 'event_stream': return <EventStreamPanel panel={panel} />
    case 'sandbox':      return <SandboxPanel panel={panel} />
    case 'checkpoint':   return <CheckpointPanel panel={panel} />
    case 'diff':         return <DiffPanel panel={panel} />
    case 'primitive':    return <PrimitivePanel panel={panel} />
  }
}

export function PanelHost({ panel }: { panel: WorkspacePanel }) {
  const focusedPanelId = useWorkspaceStore((s) => s.focusedPanelId)
  const closePanel = useWorkspaceStore((s) => s.closePanel)
  const isFocused = focusedPanelId === panel.id

  return (
    <div
      className={`flex h-full flex-col overflow-hidden rounded-xl border transition-all duration-150 ${
        isFocused
          ? 'border-[var(--blue)] shadow-[0_0_0_2px_color-mix(in_srgb,var(--blue)_18%,transparent)]'
          : 'border-[var(--border)]'
      } bg-[var(--bg-surface)]`}
    >
      {/* Header */}
      <div className="flex shrink-0 items-center justify-between border-b border-[var(--border)] px-3 py-1.5">
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-[0.2em] text-[var(--text-muted)]">
            {PANEL_LABELS[panel.type]}
          </span>
          <span className="font-mono text-[10px] text-[var(--text-muted)] opacity-50">
            {panel.id}
          </span>
        </div>
        <button
          className="flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text-primary)]"
          onClick={() => closePanel(panel.id)}
          title="Close panel"
        >
          ×
        </button>
      </div>

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-hidden">
        <PanelContent panel={panel} />
      </div>
    </div>
  )
}
