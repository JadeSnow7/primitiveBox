/**
 * replayEngine.ts
 *
 * Framework-free replay logic for timeline groups.
 *
 * Two replay modes:
 *
 *   "ui-only"  — Re-dispatch only the `ui` entries from the group.
 *                Fast. Use when you just want to see the panel layout.
 *
 *   "simulate" — Restores the sandbox to the group's checkpoint when one is
 *                available, then stubs `execution.call` entries (records them as
 *                `execution.simulated` in the timeline so the causal graph
 *                stays complete) and re-runs execution → UI mapping using the
 *                original `execution.result` data stored in the timeline.
 *                Recommended: gives a semantically complete replay.
 *
 * Invariants preserved:
 *   - If a checkpoint is present, sandbox rollback must succeed before UI replay proceeds.
 *   - Replay does NOT append new `plan` / `execution.call` / `execution.result`
 *     entries for the original group — only `ui` and `execution.simulated`
 *     entries are written locally after restore succeeds.
 *   - All replay-generated entries share the original groupId so causal tracing
 *     remains intact.
 *   - call/result matching is done by `correlationId` (not by event `id`),
 *     preserving the separation between event identity and causal linkage.
 */

import { callPrimitive } from '@/api/primitives'
import type { TimelineEntry } from '@/types/timeline'
import type { TimelineEntryInput } from '@/store/timelineStore'
import type { UIPrimitive } from '@/types/workspace'
import { mapExecutionResultToUI } from '@/lib/executionMapper'
import { resolveExecutionEntities } from '@/lib/entityTracker'
import { upsertWorkspaceEntities } from '@/store/workspaceStore'

// ─── Types ────────────────────────────────────────────────────────────────────

export type ReplayMode = 'ui-only' | 'simulate'

export interface ReplayOptions {
  groupId: string
  mode?: ReplayMode
  sandboxId?: string
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
  appendTimeline?: (entry: TimelineEntryInput) => void
  /** Injectable restore hook for testing. Defaults to calling state.restore. */
  restoreSandbox?: (sandboxId: string, checkpointId: string) => Promise<unknown>
}

export class ReplayRestoreError extends Error {
  readonly checkpointId?: string

  constructor(message: string, checkpointId?: string) {
    super(message)
    this.name = 'ReplayRestoreError'
    this.checkpointId = checkpointId
  }
}

// ─── Engine ───────────────────────────────────────────────────────────────────

/**
 * Replay a timeline group after restoring the sandbox checkpoint when available.
 * Returns the number of UI primitives that were dispatched.
 */
export async function replayTimelineGroup(opts: ReplayOptions): Promise<number> {
  const {
    groupId,
    mode = 'simulate',
    sandboxId,
    entries,
    resetWorkspace,
    workspaceDispatch,
    appendTimeline,
    restoreSandbox = (resolvedSandboxId, checkpointId) => callPrimitive(
      resolvedSandboxId,
      'state.restore',
      { checkpoint_id: checkpointId },
    ),
  } = opts

  if (mode === 'ui-only') {
    resetWorkspace()
    let dispatched = 0
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

  const checkpointId = findReplayCheckpointID(entries)
  if (checkpointId) {
    if (!sandboxId) {
      throw new ReplayRestoreError(`Replay group ${groupId} requires an active sandbox`, checkpointId)
    }
    await restoreSandbox(sandboxId, checkpointId)
  }
  resetWorkspace()

  let dispatched = 0

  // ── simulate mode: full causal replay ─────────────────────────────────────
  //
  // We scan the entry list in order. When we hit `execution.result` we:
  //   (a) find the corresponding `execution.call` entry (already passed)
  //       to retrieve params via the shared correlationId
  //   (b) re-run execution → UI mapping with the stored result
  //
  // We maintain a call map indexed by correlationId to correlate call ↔ result.
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
        // Record the call keyed by correlationId so result can reference it
        callMap.set(entry.correlationId, {
          method: entry.method,
          params: (entry.params ?? {}) as Record<string, unknown>,
        })
        // Stub: record as simulated so timeline stays causal-complete
        if (appendTimeline) {
          appendTimeline({
            kind: 'execution.simulated',
            groupId,
            correlationId: entry.correlationId,
            method: entry.method,
            params: entry.params,
          })
        }
        break
      }

      case 'execution.result': {
        // Re-run mapping using stored result data, correlating via correlationId
        const call = callMap.get(entry.correlationId)
        if (!call) break
        const resolvedEntities = resolveExecutionEntities(
          call.method,
          call.params,
          entry.result,
          entry.correlationId,
        )
        if (resolvedEntities.length > 0) {
          upsertWorkspaceEntities(resolvedEntities)
        }
        const mapped = mapExecutionResultToUI(
          call.method,
          call.params,
          entry.result,
          entry.correlationId,  // use as sourceExecutionId for dedup
          resolvedEntities,
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

function findReplayCheckpointID(entries: TimelineEntry[]): string | undefined {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i]
    if (entry.kind !== 'execution.result' || entry.method !== 'state.checkpoint') continue
    if (!entry.result || typeof entry.result !== 'object') continue
    const checkpointId = (entry.result as Record<string, unknown>)['checkpoint_id']
    if (typeof checkpointId === 'string' && checkpointId.length > 0) {
      return checkpointId
    }
  }
  return undefined
}
