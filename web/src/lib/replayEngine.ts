/**
 * replayEngine.ts
 *
 * Pure, framework-free replay logic for timeline groups.
 *
 * Two replay modes:
 *
 *   "ui-only"  — Re-dispatch only the `ui` entries from the group.
 *                Fast. Use when you just want to see the panel layout.
 *
 *   "simulate" — Also stubs `execution.call` entries (records them as
 *                `execution.simulated` in the timeline so the causal graph
 *                stays complete) and re-runs execution → UI mapping using the
 *                original `execution.result` data stored in the timeline.
 *                Recommended: gives a semantically complete replay.
 *
 * Invariants preserved:
 *   - No real network calls are ever made during replay.
 *   - Replay does NOT append new `plan` / `execution.call` / `execution.result`
 *     entries — only `ui` and `execution.simulated` entries are written.
 *   - All replay-generated entries share the original groupId so causal tracing
 *     remains intact.
 */

import type { TimelineEntry } from '@/types/timeline'
import type { UIPrimitive } from '@/types/workspace'
import { mapExecutionResultToUI } from '@/lib/executionMapper'

// ─── Types ────────────────────────────────────────────────────────────────────

export type ReplayMode = 'ui-only' | 'simulate'

export interface ReplayOptions {
  groupId: string
  mode?: ReplayMode
  /** Ordered entries for this group — pass `timelineStore.entriesByGroup(groupId)` */
  entries: TimelineEntry[]
  /** Resets the workspace to a blank state before replay */
  resetWorkspace: () => void
  /** Dispatches UI primitives to the workspace */
  workspaceDispatch: (primitives: UIPrimitive[]) => void
  /**
   * Appends a timeline entry during simulate mode.
   * Only called for `execution.simulated` stub entries.
   * Omit if you don't want any timeline side effects (pure ui-only replay).
   */
  appendTimeline?: (entry: Omit<TimelineEntry, 'id' | 'ts'>) => void
}

// ─── Engine ───────────────────────────────────────────────────────────────────

/**
 * Replay a timeline group synchronously.
 * Returns the number of UI primitives that were dispatched.
 */
export function replayTimelineGroup(opts: ReplayOptions): number {
  const {
    groupId,
    mode = 'simulate',
    entries,
    resetWorkspace,
    workspaceDispatch,
    appendTimeline,
  } = opts

  // Step 1: wipe current workspace
  resetWorkspace()

  let dispatched = 0

  if (mode === 'ui-only') {
    // ── ui-only: fast path ─────────────────────────────────────────────────
    for (const entry of entries) {
      if (entry.kind !== 'ui') continue
      const primitive = reconstructUIPrimitive(entry)
      if (primitive) {
        workspaceDispatch([primitive])
        dispatched++
      }
    }
    return dispatched
  }

  // ── simulate mode: full causal replay ─────────────────────────────────────
  //
  // We scan the entry list in order. When we hit `execution.result` we:
  //   (a) find the corresponding `execution.call` entry (already passed)
  //       to retrieve method + params
  //   (b) re-run execution → UI mapping with the stored result
  //
  // We maintain a call map indexed by id to correlate call ↔ result.
  const callMap = new Map<
    string,
    { method: string; params: Record<string, unknown> }
  >()

  for (const entry of entries) {
    switch (entry.kind) {
      case 'plan':
        // skip — planning info is informational only
        break

      case 'execution.call': {
        // Record the call so result can reference it
        callMap.set(entry.id, {
          method: entry.method,
          params: (entry.params ?? {}) as Record<string, unknown>,
        })
        // Stub: record as simulated so timeline stays causal-complete
        if (appendTimeline) {
          appendTimeline({
            kind: 'execution.simulated',
            groupId,
            method: entry.method,
            params: entry.params,
          } as Omit<TimelineEntry, 'id' | 'ts'>)
        }
        break
      }

      case 'execution.result': {
        // Re-run mapping using stored result data
        const call = callMap.get(entry.id)
        if (!call) break
        const mapped = mapExecutionResultToUI(
          call.method,
          call.params,
          entry.result,
          entry.id,
        )
        if (mapped.length > 0) {
          workspaceDispatch(mapped)
          dispatched += mapped.length
        }
        break
      }

      case 'execution.skipped':
      case 'execution.simulated':
        // skip — nothing to replay
        break

      case 'ui': {
        const primitive = reconstructUIPrimitive(entry)
        if (primitive) {
          workspaceDispatch([primitive])
          dispatched++
        }
        break
      }
    }
  }

  return dispatched
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/**
 * Reconstruct a UIPrimitive from a `ui` timeline entry.
 * Returns null if the entry's method is not a recognized UI primitive.
 */
function reconstructUIPrimitive(
  entry: Extract<TimelineEntry, { kind: 'ui' }>,
): UIPrimitive | null {
  const { method, params } = entry
  switch (method) {
    case 'ui.panel.open':
    case 'ui.panel.close':
    case 'ui.layout.split':
    case 'ui.focus.panel':
      return { method, params } as UIPrimitive
    default:
      console.warn('[replayEngine] Unknown UI primitive method during replay:', method)
      return null
  }
}
