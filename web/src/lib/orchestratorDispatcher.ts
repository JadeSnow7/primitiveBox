import { callPrimitive } from '@/api/primitives'
import { mapExecutionResultToUI } from '@/lib/executionMapper'
import { getWorkspacePanels } from '@/store/workspaceStore'
import type { OrchestratorOutput, UIPrimitive } from '@/types/workspace'
import type { TimelineState } from '@/store/timelineStore'

// ─── Types ────────────────────────────────────────────────────────────────────

export interface DispatchOptions {
  workspaceDispatch: (primitives: UIPrimitive[]) => void
  appendTimeline: TimelineState['append']
  sandboxId?: string
}

/** Result returned by `dispatchOrchestratorOutput` for use by the agent loop. */
export interface ExecutionOutcome {
  method: string
  params: Record<string, unknown>
  result?: unknown
  error?: string
  skipped?: boolean
}

export interface DispatchResult {
  outcomes: ExecutionOutcome[]
}

// ─── Dedup helper ─────────────────────────────────────────────────────────────

/**
 * Returns true if a panel with `props.sourceExecutionId === executionId` is
 * already open in the workspace.  Prevents double-opening panels when the same
 * execution result is processed more than once (e.g. optimistic retry, replay).
 */
function isPanelAlreadyOpen(executionId: string): boolean {
  const panels = getWorkspacePanels()
  return Object.values(panels).some(
    (p) => p.props['sourceExecutionId'] === executionId,
  )
}

// ─── Dispatcher ───────────────────────────────────────────────────────────────

/**
 * Execute one `OrchestratorOutput`:
 *   - execution calls  →  timeline.call → callPrimitive → timeline.result
 *                         → executionMapper → workspace panel (deduped) → timeline.ui
 *                         (or timeline.skipped when no sandbox)
 *   - ui primitives    →  workspaceDispatch + timeline.ui entry per primitive
 *
 * Returns `DispatchResult` with outcomes so the agent loop can build the next
 * iteration's context from real execution results.
 *
 * All entries share the same `groupId` for causal tracing.
 */
export async function dispatchOrchestratorOutput(
  output: OrchestratorOutput,
  opts: DispatchOptions,
): Promise<DispatchResult> {
  const { groupId, plan = [], execution = [], ui = [] } = output
  const { workspaceDispatch, appendTimeline, sandboxId } = opts
  const outcomes: ExecutionOutcome[] = []

  // ── Plan path (always first — records AI reasoning before any side effects) ─
  if (plan.length > 0) {
    appendTimeline({
      kind: 'plan',
      groupId,
      steps: plan,
    } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)
  }

  for (const call of execution) {
    // Record the intent to execute
    appendTimeline({
      kind: 'execution.call',
      groupId,
      method: call.method,
      params: call.params,
    } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)

    if (!sandboxId) {
      // Explicit skip — never silently stub
      appendTimeline({
        kind: 'execution.skipped',
        groupId,
        method: call.method,
        params: call.params,
        reason: 'no active sandbox',
      } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)
      outcomes.push({ method: call.method, params: call.params, skipped: true })
      continue
    }

    try {
      const result = await callPrimitive(sandboxId, call.method, call.params)

      // Record the raw result
      appendTimeline({
        kind: 'execution.result',
        groupId,
        method: call.method,
        params: call.params,
        result,
      } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)

      outcomes.push({ method: call.method, params: call.params, result })

      // ── Execution → UI auto-mapping ────────────────────────────────────────
      const mapped = mapExecutionResultToUI(call.method, call.params, result, call.id)

      if (mapped.length > 0) {
        // Dedup: skip if a panel for this execution result is already open.
        if (!isPanelAlreadyOpen(call.id)) {
          workspaceDispatch(mapped)

          // Record each auto-generated primitive as a ui entry (same groupId →
          // causal binding preserved).
          for (const primitive of mapped) {
            appendTimeline({
              kind: 'ui',
              groupId,
              method: primitive.method,
              params: primitive.params,
            } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)
          }
        }
      }
    } catch (err) {
      const error = err instanceof Error ? err.message : 'unknown error'
      appendTimeline({
        kind: 'execution.skipped',
        groupId,
        method: call.method,
        params: call.params,
        reason: error,
      } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)
      outcomes.push({ method: call.method, params: call.params, error })
    }
  }

  // ── UI path ────────────────────────────────────────────────────────────────
  if (ui.length > 0) {
    // Dispatch all ui primitives as one batch (workspaceStore handles ordering)
    workspaceDispatch(ui)

    // Record each primitive individually for fine-grained timeline replay
    for (const primitive of ui) {
      appendTimeline({
        kind: 'ui',
        groupId,
        method: primitive.method,
        params: primitive.params,
      } as Omit<Parameters<TimelineState['append']>[0], 'id' | 'ts'>)
    }
  }

  return { outcomes }
}
