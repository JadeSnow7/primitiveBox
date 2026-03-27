import { create } from 'zustand'
import { retainPanelProps } from '@/lib/resultRetention'
import { uiEventBus } from '@/lib/uiEventBus'
import { validateUIPrimitives } from '@/lib/uiPrimitiveValidator'
import type { ValidatedUIPrimitive } from '@/lib/uiPrimitiveValidator'
import type { LayoutNode, PanelType, SemanticRef, WorkspaceEntity, WorkspacePanel } from '@/types/workspace'

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
  activeEntities: Record<string, WorkspaceEntity>
  layout: LayoutNode
  focusedPanelId: string | null
  pendingSplitSlot: { parentPanelId: string; direction: 'horizontal' | 'vertical' } | null
}

const INITIAL_DATA: WorkspaceData = {
  panels: {},
  activeEntities: {},
  layout: { type: 'empty' },
  focusedPanelId: null,
  pendingSplitSlot: null,
}

export interface WorkspaceState extends WorkspaceData {
  dispatch: (rawInput: unknown) => void
  upsertEntities: (entities: WorkspaceEntity[]) => void
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
        activeEntities: zustandState.activeEntities,
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

  upsertEntities(entities: WorkspaceEntity[]) {
    if (entities.length === 0) return
    set((s) => {
      const activeEntities = { ...s.activeEntities }
      const now = new Date().toISOString()
      for (const incoming of entities) {
        const existing = activeEntities[incoming.id]
        if (!existing) {
          activeEntities[incoming.id] = {
            ...incoming,
            version: incoming.version > 0 ? incoming.version : 1,
            lastTouchedAt: incoming.lastTouchedAt || now,
          }
          continue
        }
        activeEntities[incoming.id] = {
          ...existing,
          ...incoming,
          metadata: { ...existing.metadata, ...incoming.metadata },
          version: existing.version + 1,
          lastTouchedAt: incoming.lastTouchedAt || now,
        }
      }
      return { activeEntities }
    })
  },

  closePanel(panelId: string) {
    set((s) => {
      const panels = { ...s.panels }
      delete panels[panelId]
      const layout = removePanel(s.layout, panelId)
      const activeEntities = pruneUnboundEntities(s.activeEntities, panels)
      uiEventBus.emit('ui.panel.closed', { panelId })
      return {
        panels,
        activeEntities,
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

export function getActiveWorkspaceEntities(): WorkspaceEntity[] {
  return Object.values(useWorkspaceStore.getState().activeEntities)
}

export function upsertWorkspaceEntities(entities: WorkspaceEntity[]): void {
  useWorkspaceStore.getState().upsertEntities(entities)
}

// ─── Pure reducer ────────────────────────────────────────────────────────────

function extractEntityIdsFromPanel(panel: WorkspacePanel): string[] {
  const set = new Set<string>()
  if (typeof panel.entityId === 'string' && panel.entityId) set.add(panel.entityId)
  if (Array.isArray(panel.entityIds)) {
    for (const id of panel.entityIds) {
      if (typeof id === 'string' && id) set.add(id)
    }
  }
  const propEntityId = panel.props['entityId']
  if (typeof propEntityId === 'string' && propEntityId) set.add(propEntityId)
  const propEntityIds = panel.props['entityIds']
  if (Array.isArray(propEntityIds)) {
    for (const id of propEntityIds) {
      if (typeof id === 'string' && id) set.add(id)
    }
  }
  return Array.from(set)
}

function pruneUnboundEntities(
  entities: Record<string, WorkspaceEntity>,
  panels: Record<string, WorkspacePanel>,
): Record<string, WorkspaceEntity> {
  const referenced = new Set<string>()
  for (const panel of Object.values(panels)) {
    for (const entityId of extractEntityIdsFromPanel(panel)) {
      referenced.add(entityId)
    }
  }
  const next: Record<string, WorkspaceEntity> = {}
  for (const [id, entity] of Object.entries(entities)) {
    if (referenced.has(id)) next[id] = entity
  }
  return next
}

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
      const primitiveEntityIds = new Set<string>()
      if (typeof primitive.params.entityId === 'string' && primitive.params.entityId) {
        primitiveEntityIds.add(primitive.params.entityId)
      }
      if (Array.isArray(primitive.params.entityIds)) {
        for (const entityId of primitive.params.entityIds) {
          if (typeof entityId === 'string' && entityId) primitiveEntityIds.add(entityId)
        }
      }
      const propEntityId = primitive.params.props?.['entityId']
      if (typeof propEntityId === 'string' && propEntityId) primitiveEntityIds.add(propEntityId)
      const propEntityIds = primitive.params.props?.['entityIds']
      if (Array.isArray(propEntityIds)) {
        for (const entityId of propEntityIds) {
          if (typeof entityId === 'string' && entityId) primitiveEntityIds.add(entityId)
        }
      }
      const entityIds = Array.from(primitiveEntityIds)
      const entityId = entityIds[0]
      const panel: WorkspacePanel = {
        id,
        type: primitive.params.type as PanelType,
        props: retainPanelProps(primitive.params.props ?? {}),
        ...(entityId ? { entityId } : {}),
        ...(entityIds.length > 0 ? { entityIds } : {}),
        ...(entityId
          ? { entityVersionSnapshot: state.activeEntities[entityId]?.version ?? 0 }
          : {}),
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
      return {
        ...state,
        panels,
        activeEntities: pruneUnboundEntities(state.activeEntities, panels),
        layout,
        focusedPanelId: id,
        pendingSplitSlot: null,
      }
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
        activeEntities: pruneUnboundEntities(state.activeEntities, panels),
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
