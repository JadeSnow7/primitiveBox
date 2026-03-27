import { create } from 'zustand'
import { listPrimitives } from '@/api/primitives'
import type { PrimitiveSchema } from '@/types/primitive'

type PrimitiveCatalogStatus = 'idle' | 'loading' | 'ready' | 'error'

interface PrimitiveState {
  status: PrimitiveCatalogStatus
  primitives: PrimitiveSchema[]
  primitivesByName: Record<string, PrimitiveSchema>
  error: string | null
  load: () => Promise<void>
  getPrimitive: (name: string) => PrimitiveSchema | null
  reset: () => void
}

const INITIAL_STATE = {
  status: 'idle' as PrimitiveCatalogStatus,
  primitives: [] as PrimitiveSchema[],
  primitivesByName: {} as Record<string, PrimitiveSchema>,
  error: null as string | null,
}

export const usePrimitiveStore = create<PrimitiveState>((set, get) => ({
  ...INITIAL_STATE,

  async load() {
    if (get().status === 'loading' || get().status === 'ready') {
      return
    }

    set({ status: 'loading', error: null })
    try {
      const primitives = await listPrimitives()
      const primitivesByName = Object.fromEntries(primitives.map((primitive) => [primitive.name, primitive]))
      set({
        status: 'ready',
        primitives,
        primitivesByName,
        error: null,
      })
    } catch (error) {
      set({
        status: 'error',
        error: error instanceof Error ? error.message : 'Failed to load primitive catalog',
        primitives: [],
        primitivesByName: {},
      })
    }
  },

  getPrimitive(name) {
    return get().primitivesByName[name] ?? null
  },

  reset() {
    set(INITIAL_STATE)
  },
}))
