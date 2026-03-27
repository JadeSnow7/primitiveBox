import { create } from 'zustand'
import type { ComponentType } from 'react'
import { DataTableWidget } from '@/components/workspace/widgets/DataTableWidget'
import { MarkdownWidget } from '@/components/workspace/widgets/MarkdownWidget'
import { RawJsonWidget } from '@/components/workspace/widgets/RawJsonWidget'
import type { PrimitiveSchema } from '@/types/primitive'
import type { WorkspacePanel } from '@/types/workspace'

export interface UIWidgetProps {
  panel: WorkspacePanel
  result: unknown
  primitive: PrimitiveSchema | null
}

export type UIWidgetComponent = ComponentType<UIWidgetProps>

export interface UIPluginRenderer {
  id: string
  component: UIWidgetComponent
  primitive?: string
  namespace?: string
  uiLayoutHint?: string
  outputSchemaMatcher?: (schema: unknown) => boolean
  priority?: number
}

export interface UIResolveInput {
  primitiveName?: string
  uiLayoutHint?: string
  outputSchema?: unknown
}

const DEFAULT_RENDERERS: UIPluginRenderer[] = [
  { id: 'hint-table', component: DataTableWidget, uiLayoutHint: 'table', priority: 100 },
  { id: 'hint-markdown', component: MarkdownWidget, uiLayoutHint: 'markdown', priority: 90 },
  { id: 'fallback-raw-json', component: RawJsonWidget, priority: -1 },
]

interface UIRegistryState {
  renderers: UIPluginRenderer[]
  registerRenderer: (renderer: UIPluginRenderer) => void
  resolveView: (input: UIResolveInput) => UIWidgetComponent
  reset: () => void
}

function primitiveNamespace(primitiveName?: string): string | null {
  if (!primitiveName) return null
  const idx = primitiveName.indexOf('.')
  return idx > 0 ? primitiveName.slice(0, idx) : primitiveName
}

function matches(renderer: UIPluginRenderer, input: UIResolveInput): boolean {
  const primitive = input.primitiveName ?? ''
  const namespace = primitiveNamespace(input.primitiveName)
  if (renderer.primitive && renderer.primitive !== primitive) return false
  if (renderer.namespace && renderer.namespace !== namespace) return false
  if (renderer.uiLayoutHint && renderer.uiLayoutHint !== input.uiLayoutHint) return false
  if (renderer.outputSchemaMatcher && !renderer.outputSchemaMatcher(input.outputSchema)) return false
  return true
}

export const useUIRegistryStore = create<UIRegistryState>((set, get) => ({
  renderers: DEFAULT_RENDERERS,

  registerRenderer(renderer) {
    set((state) => ({
      renderers: [...state.renderers.filter((item) => item.id !== renderer.id), renderer],
    }))
  },

  resolveView(input) {
    const matched = get()
      .renderers
      .filter((renderer) => matches(renderer, input))
      .sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0))
    return (matched[0] ?? DEFAULT_RENDERERS[DEFAULT_RENDERERS.length - 1]).component
  },

  reset() {
    set({ renderers: DEFAULT_RENDERERS })
  },
}))
