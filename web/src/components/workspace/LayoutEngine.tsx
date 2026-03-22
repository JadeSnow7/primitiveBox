import { PanelHost } from './PanelHost'
import { useWorkspaceStore } from '@/store/workspaceStore'
import type { LayoutNode } from '@/types/workspace'

interface LayoutEngineProps {
  node: LayoutNode
  depth?: number
}

export function LayoutEngine({ node, depth = 0 }: LayoutEngineProps) {
  const panels = useWorkspaceStore((s) => s.panels)

  if (node.type === 'empty') {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-[var(--border)] text-[var(--text-muted)]">
        <div className="text-3xl opacity-20">⬚</div>
        <div className="text-[12px]">No panels open</div>
      </div>
    )
  }

  if (node.type === 'panel') {
    const panel = panels[node.panelId]
    if (!panel) return null
    return <PanelHost panel={panel} />
  }

  if (node.type === 'split') {
    const isHorizontal = node.direction === 'horizontal'
    return (
      <div
        className={`flex min-h-0 h-full gap-2 ${isHorizontal ? 'flex-row' : 'flex-col'}`}
        style={{ '--split-depth': depth } as React.CSSProperties}
      >
        <div className="min-h-0 flex-1 overflow-hidden">
          <LayoutEngine node={node.children[0]} depth={depth + 1} />
        </div>
        <div
          className={`shrink-0 rounded-full bg-[var(--border)] ${isHorizontal ? 'w-[1px]' : 'h-[1px]'}`}
        />
        <div className="min-h-0 flex-1 overflow-hidden">
          <LayoutEngine node={node.children[1]} depth={depth + 1} />
        </div>
      </div>
    )
  }

  if (node.type === 'tabs') {
    return <TabsNode node={node} depth={depth} />
  }

  return null
}

function TabsNode({ node, depth }: { node: Extract<LayoutNode, { type: 'tabs' }>; depth: number }) {
  const panels = useWorkspaceStore((s) => s.panels)
  // Use the store's focus to drive active tab
  const focusedPanelId = useWorkspaceStore((s) => s.focusedPanelId)
  const activeId = node.panels.includes(focusedPanelId ?? '') ? (focusedPanelId ?? node.active) : node.active

  const activePanel = panels[activeId]

  return (
    <div className="flex h-full flex-col overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-surface)]">
      {/* Tab bar */}
      <div className="flex shrink-0 items-end gap-0.5 overflow-x-auto border-b border-[var(--border)] px-2 pt-1">
        {node.panels.map((id) => {
          const p = panels[id]
          if (!p) return null
          const isActive = id === activeId
          return (
            <button
              key={id}
              className={`shrink-0 rounded-t-md px-3 py-1 text-[11px] transition-colors ${
                isActive
                  ? 'bg-[var(--bg-surface)] text-[var(--text-primary)] font-medium'
                  : 'text-[var(--text-muted)] hover:bg-[var(--bg-subtle)]'
              }`}
            >
              {p.type}
            </button>
          )
        })}
      </div>
      {/* Active panel content */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {activePanel ? <LayoutEngine node={{ type: 'panel', panelId: activeId }} depth={depth + 1} /> : null}
      </div>
    </div>
  )
}
