import { create } from 'zustand'
import { retainExecutionPayload } from '@/lib/resultRetention'
import type { TimelineEntry } from '@/types/timeline'

const MAX_ENTRIES = 100
let seq = 0

function nextId(): string {
  return `tl-${++seq}`
}

function now(): string {
  return new Date().toISOString()
}

/**
 * Distributive helper: strips `id` and `ts` from each union member individually.
 * Using plain `Omit<TimelineEntry, 'id' | 'ts'>` would collapse the discriminated
 * union into an intersection, losing all member-specific fields.
 */
export type TimelineEntryInput = TimelineEntry extends infer T
  ? T extends { id: string; ts: string }
    ? Omit<T, 'id' | 'ts'>
    : never
  : never

export interface TimelineState {
  entries: TimelineEntry[]
  /** Append a new entry; `id` and `ts` are always auto-generated. */
  append(entry: TimelineEntryInput): void
  clear(): void
  entriesByGroup(groupId: string): TimelineEntry[]
}

export const useTimelineStore = create<TimelineState>((set, get) => ({
  entries: [],

  append(partial) {
    const boundedPartial = (() => {
      switch (partial.kind) {
        case 'execution.call':
        case 'execution.pending_review':
        case 'execution.rejected':
        case 'execution.simulated':
        case 'ui':
          return { ...partial, params: retainExecutionPayload(partial.params) }
        case 'execution.result':
          return { ...partial, result: retainExecutionPayload(partial.result) }
        default:
          return partial
      }
    })()
    const entry = { ...boundedPartial, id: nextId(), ts: now() } as TimelineEntry
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

export function getTimelineEntries(): TimelineEntry[] {
  return useTimelineStore.getState().entries
}
