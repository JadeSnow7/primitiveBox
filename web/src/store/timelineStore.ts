import { create } from 'zustand'
import type { TimelineEntry } from '@/types/timeline'

const MAX_ENTRIES = 100
let seq = 0

function nextId(): string {
  return `tl-${++seq}`
}

function now(): string {
  return new Date().toISOString()
}

export interface TimelineState {
  entries: TimelineEntry[]
  append(entry: Omit<TimelineEntry, 'id' | 'ts'>): void
  clear(): void
  entriesByGroup(groupId: string): TimelineEntry[]
}

export const useTimelineStore = create<TimelineState>((set, get) => ({
  entries: [],

  append(partial) {
    const entry = { ...partial, id: nextId(), ts: now() } as TimelineEntry
    set((s) => ({
      entries:
        s.entries.length >= MAX_ENTRIES
          ? [...s.entries.slice(1), entry]
          : [...s.entries, entry],
    }))
  },

  clear() {
    set({ entries: [] })
  },

  entriesByGroup(groupId: string) {
    return get().entries.filter((e) => e.groupId === groupId)
  },
}))
