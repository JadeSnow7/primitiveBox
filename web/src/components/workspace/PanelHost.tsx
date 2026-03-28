import { resolvePanelView } from '@/lib/panelRegistry'
import { useWorkspaceStore } from '@/store/workspaceStore'
import type { PanelType, WorkspacePanel } from '@/types/workspace'

const PANEL_LABELS: Record<PanelType, string> = {
  trace: 'Trace',
  event_stream: 'Event Stream',
  sandbox: 'Sandbox',
  checkpoint: 'Checkpoint',
  diff: 'Diff',
  primitive: 'Primitives',
  goal: 'Goal',
}

function PanelContent({ panel }: { panel: WorkspacePanel }) {
  const View = resolvePanelView(panel.type)
  return <View panel={panel} />
}

export function PanelHost({ panel }: { panel: WorkspacePanel }) {
  const focusedPanelId = useWorkspaceStore((s) => s.focusedPanelId)
  const activeEntities = useWorkspaceStore((s) => s.activeEntities)
  const closePanel = useWorkspaceStore((s) => s.closePanel)
  const isFocused = focusedPanelId === panel.id
  const entityId = panel.entityId ?? (typeof panel.props['entityId'] === 'string' ? panel.props['entityId'] : undefined)
  const boundEntity = entityId ? activeEntities[entityId] : undefined
  const isStale = Boolean(
    boundEntity &&
      panel.entityVersionSnapshot !== undefined &&
      panel.entityVersionSnapshot < boundEntity.version,
  )

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
          {boundEntity && (
            <span className="rounded border border-[var(--border)] px-1.5 py-0.5 font-mono text-[9px] text-[var(--text-muted)]">
              {boundEntity.type}:{boundEntity.uri}
            </span>
          )}
          {isStale && (
            <span className="rounded border border-[var(--amber)]/40 bg-[var(--amber)]/15 px-1.5 py-0.5 text-[9px] font-medium text-[var(--amber)]">
              stale
            </span>
          )}
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
