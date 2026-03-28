import { callPrimitive } from '@/api/primitives'
import { mapExecutionResultToUI } from '@/lib/executionMapper'
import { resolveExecutionEntities } from '@/lib/entityTracker'
import {
  PrimitiveCatalogUnavailableError,
  requiresHumanReview,
  resolvePrimitiveIntent,
} from '@/lib/primitiveIntent'
import { useOrchestratorStore, type ReviewDecision } from '@/store/orchestratorStore'
import { getWorkspacePanels, upsertWorkspaceEntities } from '@/store/workspaceStore'
import { getTimelineEntries } from '@/store/timelineStore'
import type { OrchestratorOutput, UIPrimitive } from '@/types/workspace'
import type { TimelineState } from '@/store/timelineStore'

// ─── Types ────────────────────────────────────────────────────────────────────

export interface DispatchOptions {
  workspaceDispatch: (primitives: UIPrimitive[]) => void
  appendTimeline: TimelineState['append']
  sandboxId?: string
  /**
   * Propagated from the agent loop. When fired, any in-progress
   * requestReview() suspension is cancelled (resolved as 'rejected') so the
   * loop can observe the aborted signal at its next iteration boundary.
   */
  signal?: AbortSignal
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

const HIGH_RISK_METHODS = new Set<string>([
  'fs.write',
  'shell.exec',
  'verify.test',
  'db.execute',
])

function makeSyntheticCallId(prefix: string, groupId: string): string {
  return `${prefix}-${groupId}-${Math.random().toString(36).slice(2, 8)}`
}

function extractCheckpointID(result: unknown): string | undefined {
  if (!result || typeof result !== 'object') return undefined
  const checkpointId = (result as Record<string, unknown>)['checkpoint_id']
  return typeof checkpointId === 'string' && checkpointId.length > 0 ? checkpointId : undefined
}

function findLatestCheckpointID(entries: ReturnType<typeof getTimelineEntries>): string | undefined {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i]
    if (entry.kind !== 'execution.result' || entry.method !== 'state.checkpoint') continue
    const checkpointId = extractCheckpointID(entry.result)
    if (checkpointId) return checkpointId
  }
  return undefined
}

function normalizeExecutionCalls(
  output: OrchestratorOutput,
  existingEntries: ReturnType<typeof getTimelineEntries>,
) {
  const execution = output.execution ?? []
  const normalized = [...execution]
  const hasCheckpoint = normalized.some((call) => call.method === 'state.checkpoint')
  const needsCheckpoint = normalized.some((call) => HIGH_RISK_METHODS.has(call.method))

  if (needsCheckpoint && !hasCheckpoint) {
    normalized.unshift({
      id: makeSyntheticCallId('auto-checkpoint', output.groupId),
      method: 'state.checkpoint',
      params: {
        label: `auto:${output.groupId}`,
      },
    })
  }

  const latestCheckpointId = findLatestCheckpointID(existingEntries)

  return normalized.map((call) => {
    if (call.method !== 'state.restore') return call
    if (typeof call.params['checkpoint_id'] === 'string' && call.params['checkpoint_id']) {
      return call
    }
    if (!latestCheckpointId) return call
    return {
      ...call,
      params: {
        ...call.params,
        checkpoint_id: latestCheckpointId,
      },
    }
  })
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
 * call/result/skipped entries share `correlationId = call.id` for replay correlation.
 */
export async function dispatchOrchestratorOutput(
  output: OrchestratorOutput,
  opts: DispatchOptions,
): Promise<DispatchResult> {
  const { groupId, plan = [], ui = [] } = output
  const { workspaceDispatch, appendTimeline, sandboxId, signal } = opts
  const outcomes: ExecutionOutcome[] = []
  const execution = normalizeExecutionCalls(output, getTimelineEntries())

  // ── Plan path (always first — records AI reasoning before any side effects) ─
  if (plan.length > 0) {
    appendTimeline({
      kind: 'plan',
      groupId,
      steps: plan,
    })
  }

  for (const call of execution) {
    // correlationId ties timeline.call ↔ timeline.result for replay
    const correlationId = call.id

    // Record the intent to execute
    appendTimeline({
      kind: 'execution.call',
      groupId,
      correlationId,
      method: call.method,
      params: call.params,
    })

    let intent
    try {
      intent = resolvePrimitiveIntent(call.method)
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Primitive catalog unavailable'
      appendTimeline({
        kind: 'execution.skipped',
        groupId,
        correlationId,
        method: call.method,
        params: call.params,
        reason: 'validation_failed',
        message,
      })
      outcomes.push({ method: call.method, params: call.params, error: message })
      if (error instanceof PrimitiveCatalogUnavailableError) {
        throw error
      }
      throw new PrimitiveCatalogUnavailableError(message)
    }

    if (requiresHumanReview(call.method)) {
      appendTimeline({
        kind: 'execution.pending_review',
        groupId,
        correlationId,
        method: call.method,
        params: call.params,
        risk_level: intent.risk_level,
        reversible: intent.reversible,
        side_effect: intent.side_effect,
      })

      const reviewPromise = useOrchestratorStore.getState().requestReview({
        groupId,
        correlationId,
        method: call.method,
        params: call.params,
        intent,
      })

      let decision: ReviewDecision
      if (signal?.aborted) {
        useOrchestratorStore.getState().rejectPendingReview()
        decision = 'rejected'
      } else if (signal) {
        // Race the human-review promise against the abort signal so the loop
        // is not stuck indefinitely when the component unmounts or the caller
        // cancels. Resolving as 'rejected' keeps timeline state consistent.
        const abortPromise = new Promise<ReviewDecision>((resolve) => {
          signal.addEventListener(
            'abort',
            () => {
              useOrchestratorStore.getState().rejectPendingReview()
              resolve('rejected')
            },
            { once: true },
          )
        })
        decision = await Promise.race([reviewPromise, abortPromise])
      } else {
        decision = await reviewPromise
      }

      if (decision === 'rejected') {
        const message = `Execution completely REJECTED by Human Reviewer. Re-evaluate your plan.`
        appendTimeline({
          kind: 'execution.rejected',
          groupId,
          correlationId,
          method: call.method,
          params: call.params,
          decision,
          reason: message,
        })
        outcomes.push({
          method: call.method,
          params: call.params,
          error: message,
        })
        continue
      }
    }

    if (!sandboxId) {
      // Explicit skip — never silently stub
      appendTimeline({
        kind: 'execution.skipped',
        groupId,
        correlationId,
        method: call.method,
        params: call.params,
        reason: 'no_sandbox',
      })
      outcomes.push({ method: call.method, params: call.params, skipped: true })
      continue
    }

    try {
      const result = await callPrimitive(sandboxId, call.method, call.params)
      const resolvedEntities = resolveExecutionEntities(call.method, call.params, result, correlationId)
      if (resolvedEntities.length > 0) {
        upsertWorkspaceEntities(resolvedEntities)
      }
      const entityIds = resolvedEntities.map((entity) => entity.id)

      // Record the raw result — keep method for readability; correlationId links back to call
      appendTimeline({
        kind: 'execution.result',
        groupId,
        correlationId,
        method: call.method,
        result,
        ...(entityIds.length > 0 ? { entityIds } : {}),
      })

      outcomes.push({ method: call.method, params: call.params, result })

      // ── Execution → UI auto-mapping ────────────────────────────────────────
      const mapped = mapExecutionResultToUI(
        call.method,
        call.params,
        result,
        correlationId,
        resolvedEntities,
      )

      if (mapped.length > 0) {
        // Dedup: skip if a panel for this execution result is already open.
        if (!isPanelAlreadyOpen(correlationId)) {
          workspaceDispatch(mapped)

          // Record each auto-generated primitive as a ui entry (same groupId →
          // causal binding preserved).
          for (const primitive of mapped) {
            appendTimeline({
              kind: 'ui',
              groupId,
              method: primitive.method,
              params: primitive.params,
            })
          }
        }
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : 'unknown error'
      appendTimeline({
        kind: 'execution.skipped',
        groupId,
        correlationId,
        method: call.method,
        params: call.params,
        reason: 'unsupported',
        message,
      })
      outcomes.push({ method: call.method, params: call.params, error: message })
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
      })
    }
  }

  return { outcomes }
}
