import { create } from 'zustand'

interface UIState {
  selectedEventId: string | null
  detailOpen: boolean
  gatewayStatus: 'checking' | 'online' | 'offline'
  createDialogOpen: boolean
  setSelectedEventId: (id: string | null) => void
  setDetailOpen: (open: boolean) => void
  setGatewayStatus: (status: UIState['gatewayStatus']) => void
  setCreateDialogOpen: (open: boolean) => void
}

export const useUIStore = create<UIState>((set) => ({
  selectedEventId: null,
  detailOpen: true,
  gatewayStatus: 'checking',
  createDialogOpen: false,
  setSelectedEventId: (id) => set({ selectedEventId: id }),
  setDetailOpen: (open) => set({ detailOpen: open }),
  setGatewayStatus: (status) => set({ gatewayStatus: status }),
  setCreateDialogOpen: (open) => set({ createDialogOpen: open })
}))
