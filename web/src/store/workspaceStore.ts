import { create } from 'zustand'
import { uiEventBus } from '@/lib/uiEventBus'
import { validateUIPrimitives } from '@/lib/uiPrimitiveValidator'
import type { ValidatedUIPrimitive } from '@/lib/uiPrimitiveValidator'
import type { LayoutNode, PanelType, SemanticRef, WorkspacePanel } from '@/types/workspace'

// ─── Constants ───────────────────────────────────────────────────────────────

const MAX_PANELS = 6
const MAX_SPLIT_DEPTH = 3

// ─── Helpers ─────────────────────────────────────────────────────────────────

let panelCounter = 0
function nextPanelId(): string {
  return `p-${++panelCounter}`
}

/** Resolve a SemanticRef to a real panelId given the current panels map */
function resolveSemanticRef(
  ref: SemanticRef,
  panels: Record<string, WorkspacePanel>,
): string | null {
  const matching = Object.values(panels).filter((p) => p.type === ref.type)
  const idx = ref.index ?? 0
  return matching[idx]?.id ?? null
}

/** Return the depth of split nesting in a layout tree */
function splitDepth(node: LayoutNode): number {
  if (node.type === 'split') {
    return 1 + Math.max(splitDepth(node.children[0]), splitDepth(node.children[1]))
  }
  return 0
}

/** Replace the first occurrence of { type:'panel', panelId } with newNode */
function replacePanel(layout: LayoutNode, panelId: string, newNode: LayoutNode): LayoutNode {
  if (layout.type === 'panel' && layout.panelId === panelId) return newNode
  if (layout.type === 'split') {
    return {
      ...layout,
      children: [
        replacePanel(layout.children[0], panelId, newNode),
        replacePanel(layout.children[1], panelId, newNode),
      ],
    }
  }
  if (layout.type === 'tabs') {
    // tabs doesn't host splits; return as-is
    return layout
  }
  return layout
}

/** Remove a panelId from the layout tree; simplifies single-child splits */
function removePanel(layout: LayoutNode, panelId: string): LayoutNode {
  if (layout.type === 'panel') {
    return layout.panelId === panelId ? { type: 'empty' } : layout
  }
  if (layout.type === 'split') {
    const left = removePanel(layout.children[0], panelId)
    const right = removePanel(layout.children[1], panelId)
    // Collapse split if one side is empty
    if (left.type === 'empty') return right
    if (right.type === 'empty') return left
    return { ...layout, children: [left, right] }
  }
  if (layout.type === 'tabs') {
    const panels = layout.panels.filter((id) => id !== panelId)
    if (panels.length === 0) return { type: 'empty' }
    const active = layout.active === panelId ? panels[0] : layout.active
    return { type: 'tabs', panels, active }
  }
  return layout
}

// ─── Separated data state (no actions) ──────────────────────────────────────

export interface WorkspaceData {
  panels: Record<string, WorkspacePanel>
  layout: LayoutNode
  focusedPanelId: string | null
  pendingSplitSlot: { parentPanelId: string; direction: 'horizontal' | 'vertical' } | null
}

const INITIAL_DATA: WorkspaceData = {
  panels: {},
  layout: { type: 'empty' },
  focusedPanelId: null,
  pendingSplitSlot: null,
}

export interface WorkspaceState extends WorkspaceData {
  dispatch: (rawInput: unknown) => void
  closePanel: (panelId: string) => void
  reset: () => void
  /**
   * Explicit named alias used by the replay engine — semantically equivalent
   * to `reset()` but self-documenting at call sites outside React context.
   */
  resetWorkspace: () => void
}

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  ...INITIAL_DATA,

  dispatch(rawInput: unknown) {
    const parsed = validateUIPrimitives(rawInput)
    if (!parsed.success) {
      console.error('[workspace] Validation failed:', parsed.error, rawInput)
      uiEventBus.emit('ui.primitive.rejected', { reason: parsed.error })
      return
    }

    // Use set with an updater to keep data and actions separate
    set((zustandState) => {
      // Extract only the data portion
      let data: WorkspaceData = {
        panels: zustandState.panels,
        layout: zustandState.layout,
        focusedPanelId: zustandState.focusedPanelId,
        pendingSplitSlot: zustandState.pendingSplitSlot,
      }
      for (const primitive of parsed.data) {
        data = applyPrimitive(data, primitive)
      }
      return data
    })
  },

  closePanel(panelId: string) {
    set((s) => {
      const panels = { ...s.panels }
      delete panels[panelId]
      const layout = removePanel(s.layout, panelId)
      uiEventBus.emit('ui.panel.closed', { panelId })
      return {
        panels,
        layout,
        focusedPanelId: s.focusedPanelId === panelId ? null : s.focusedPanelId,
      }
    })
  },

  reset() {
    set(INITIAL_DATA)
  },

  resetWorkspace() {
    set(INITIAL_DATA)
  },
}))

/**
 * Non-reactive snapshot of the current panels map.
 * Safe to call from non-React contexts (dispatcher, replay engine) without
 * subscribing to the store.  Avoids the circular-reference lint that would
 * occur if `getPanels` were defined inside `create()`.
 */
export function getWorkspacePanels(): Record<string, WorkspacePanel> {
  return useWorkspaceStore.getState().panels
}
// ─── Pure reducer ────────────────────────────────────────────────────────────

function applyPrimitive(state: WorkspaceData, primitive: ValidatedUIPrimitive): WorkspaceData {
  switch (primitive.method) {
    case 'ui.panel.open': {
      if (Object.keys(state.panels).length >= MAX_PANELS) {
        uiEventBus.emit('ui.primitive.rejected', {
          reason: `Max ${MAX_PANELS} panels reached`,
          method: primitive.method,
        })
        return state
      }

      const id = nextPanelId()
      const panel: WorkspacePanel = {
        id,
        type: primitive.params.type as PanelType,
        props: primitive.params.props ?? {},
      }
      const panels = { ...state.panels, [id]: panel }

      let layout: LayoutNode

      if (state.pendingSplitSlot) {
        // Fill the slot created by ui.layout.split
        const { parentPanelId, direction } = state.pendingSplitSlot
        if (splitDepth(state.layout) < MAX_SPLIT_DEPTH) {
          layout = replacePanel(state.layout, parentPanelId, {
            type: 'split',
            direction,
            children: [{ type: 'panel', panelId: parentPanelId }, { type: 'panel', panelId: id }],
          })
        } else {
          uiEventBus.emit('ui.primitive.rejected', { reason: 'Max split depth reached', panelId: id })
          layout = state.layout.type === 'empty'
            ? { type: 'panel', panelId: id }
            : state.layout
        }
      } else if (state.layout.type === 'empty') {
        layout = { type: 'panel', panelId: id }
      } else {
        // Append to tabs at root, or wrap root in tabs
        if (state.layout.type === 'tabs') {
          layout = { type: 'tabs', panels: [...state.layout.panels, id], active: id }
        } else {
          // Current root becomes first tab
          const existing = Object.values(panels)
            .filter((p) => p.id !== id)
            .find((p) => {
              // find the panelId that is the current root leaf
              const root = state.layout
              return root.type === 'panel' && root.panelId === p.id
            })
          if (existing && state.layout.type === 'panel') {
            layout = { type: 'tabs', panels: [state.layout.panelId, id], active: id }
          } else {
            // Nested split: just wrap in a new horizontal split at the root
            layout = {
              type: 'split',
              direction: 'horizontal',
              children: [state.layout, { type: 'panel', panelId: id }],
            }
          }
        }
      }

      uiEventBus.emit('ui.panel.opened', { panelId: id, type: panel.type })
      return { ...state, panels, layout, focusedPanelId: id, pendingSplitSlot: null }
    }

    case 'ui.panel.close': {
      const panelId = resolveSemanticRef(primitive.params.target, state.panels)
      if (!panelId) return state
      const panels = { ...state.panels }
      delete panels[panelId]
      const layout = removePanel(state.layout, panelId)
      uiEventBus.emit('ui.panel.closed', { panelId })
      return {
        ...state,
        panels,
        layout,
        focusedPanelId: state.focusedPanelId === panelId ? null : state.focusedPanelId,
      }
    }

    case 'ui.layout.split': {
      // Don't actually split now — record the "pending slot" so next ui.panel.open fills it
      const panelId = resolveSemanticRef(primitive.params.target, state.panels)
      if (!panelId) return state

      if (splitDepth(state.layout) >= MAX_SPLIT_DEPTH) {
        uiEventBus.emit('ui.primitive.rejected', {
          reason: 'Max split depth reached',
          method: primitive.method,
        })
        return state
      }

      uiEventBus.emit('ui.layout.changed', { action: 'split-pending', panelId, direction: primitive.params.direction })
      return {
        ...state,
        pendingSplitSlot: { parentPanelId: panelId, direction: primitive.params.direction },
      }
    }

    case 'ui.focus.panel': {
      const panelId = resolveSemanticRef(primitive.params.target, state.panels)
      if (!panelId) return state
      uiEventBus.emit('ui.focus.changed', { panelId })
      return { ...state, focusedPanelId: panelId }
    }
  }
}
