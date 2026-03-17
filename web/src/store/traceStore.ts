import { create } from 'zustand'
import type { TraceEvent, TraceMetrics } from '@/types/trace'

const MAX_EVENTS = 200

interface TraceState {
  events: TraceEvent[]
  selectedId: string | null
  streamState: 'idle' | 'blocked' | 'ready'
  blockReason: string | null
  addEvent: (e: TraceEvent) => void
  setEvents: (events: TraceEvent[]) => void
  setSelected: (id: string | null) => void
  setBlocked: (reason: string) => void
  setReady: () => void
  clearEvents: () => void
  getMetrics: () => TraceMetrics
}

export const useTraceStore = create<TraceState>((set, get) => ({
  events: [],
  selectedId: null,
  streamState: 'idle',
  blockReason: null,
  addEvent: (e) =>
    set((s) => ({
      events: [e, ...s.events].slice(0, MAX_EVENTS)
    })),
  setEvents: (events) => set({ events: events.slice(0, MAX_EVENTS) }),
  setSelected: (id) => set({ selectedId: id }),
  setBlocked: (reason) => set({ streamState: 'blocked', blockReason: reason }),
  setReady: () => set({ streamState: 'ready', blockReason: null }),
  clearEvents: () => set({ events: [], selectedId: null, streamState: 'idle', blockReason: null }),
  getMetrics: () => {
    const { events } = get()
    return {
      total: events.length,
      checkpoints: events.filter((e) => e.checkpoint_id).length,
      rollbacks: events.filter((e) => e.recovery_path === 'rollback').length,
      failures: events.filter((e) => e.strategy_outcome === 'failed').length
    }
  }
}))
