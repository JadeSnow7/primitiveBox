import { create } from 'zustand'
import { APIError } from '@/api/client'
import {
  createSandbox as createSandboxRequest,
  destroySandbox as destroySandboxRequest,
  getSandbox,
  listSandboxes
} from '@/api/sandboxes'
import type { Sandbox } from '@/types/sandbox'

interface CreateParams {
  driver: 'docker'
  workspace: string
  ttl: number
}

interface SandboxState {
  sandboxes: Sandbox[]
  selectedId: string | null
  loading: boolean
  error: string | null
  capabilityNotice: string | null
  load: () => Promise<void>
  refreshSelected: () => Promise<void>
  select: (id: string | null) => void
  create: (params: CreateParams) => Promise<void>
  destroy: (id: string) => Promise<void>
}

function formatCapabilityError(error: unknown, fallback: string): string {
  if (error instanceof APIError && [404, 405, 501].includes(error.status)) {
    return fallback
  }
  if (error instanceof Error) return error.message
  return 'Unknown request error'
}

export const useSandboxStore = create<SandboxState>((set, get) => ({
  sandboxes: [],
  selectedId: null,
  loading: false,
  error: null,
  capabilityNotice: null,
  load: async () => {
    set({ loading: true, error: null })
    try {
      const sandboxes = await listSandboxes()
      const selectedId = get().selectedId && sandboxes.some((item) => item.id === get().selectedId)
        ? get().selectedId
        : sandboxes[0]?.id ?? null
      set({ sandboxes, selectedId, loading: false })
    } catch (error) {
      set({
        loading: false,
        error: error instanceof Error ? error.message : 'Failed to load sandboxes'
      })
    }
  },
  refreshSelected: async () => {
    const { selectedId, sandboxes } = get()
    if (!selectedId) return
    try {
      const sandbox = await getSandbox(selectedId)
      set({
        sandboxes: sandboxes.map((item) => (item.id === sandbox.id ? sandbox : item))
      })
    } catch {
      // Keep current state if detail refresh fails.
    }
  },
  select: (id) => set({ selectedId: id }),
  create: async (params) => {
    try {
      const created = await createSandboxRequest(params)
      set((state) => ({
        sandboxes: [created, ...state.sandboxes],
        selectedId: created.id,
        capabilityNotice: null
      }))
    } catch (error) {
      set({
        capabilityNotice: formatCapabilityError(error, '后端当前未提供 sandbox 创建接口。')
      })
    }
  },
  destroy: async (id) => {
    try {
      await destroySandboxRequest(id)
      set((state) => ({
        sandboxes: state.sandboxes.filter((item) => item.id !== id),
        selectedId: state.selectedId === id ? state.sandboxes.find((item) => item.id !== id)?.id ?? null : state.selectedId,
        capabilityNotice: null
      }))
    } catch (error) {
      set({
        capabilityNotice: formatCapabilityError(error, '后端当前未提供 sandbox 销毁接口。')
      })
    }
  }
}))
