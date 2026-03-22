/**
 * agentLoop.ts
 *
 * Autonomous agent loop: PLAN → ACT → OBSERVE → REPLAN → VERIFY
 *
 * Each iteration:
 *   1. Calls the Executor AI (LLM or local fallback) → OrchestratorOutput
 *   2. Dispatches output (execution + UI), collecting outcomes.
 *   3. Injects outcomes back into context as `lastExecution`.
 *   4. Checks `status` + confidence threshold:
 *      - 'continue'          → next iteration
 *      - 'done' + low conf   → continue (confidence override)
 *      - 'done' + ok conf    → run Verification Agent
 *        - verified: true    → truly done
 *        - verified: false   → continue with missing[] context
 *   5. maxIterations guard prevents runaway loops.
 *
 * Design:
 *   - Pure async; no React dependency.
 *   - Cancellable via AbortSignal.
 *   - Progress via optional callbacks.
 */

import {
  callOrchestratorAI,
  buildOrchestratorContext,
  type OrchestratorContext,
} from '@/api/uiPrimitives'
import {
  dispatchOrchestratorOutput,
  type DispatchOptions,
  type ExecutionOutcome,
} from '@/lib/orchestratorDispatcher'
import { verifyGoal, type VerificationContext } from '@/lib/verificationAgent'
import { useWorkspaceStore } from '@/store/workspaceStore'
import type { OrchestratorOutput } from '@/types/workspace'
import type { VerificationResult } from '@/types/workspace'
import type { TimelineEntry } from '@/types/timeline'

// ─── Types ────────────────────────────────────────────────────────────────────

export interface AgentLoopOptions extends DispatchOptions {
  /** Maximum number of iterations before hard-stopping (default: 10). */
  maxIterations?: number
  /**
   * Confidence threshold below which a 'done' signal is overridden and the
   * loop continues. Defaults to 0.5.
   */
  confidenceThreshold?: number
  /**
   * Whether to run the Verification Agent after the Executor reports 'done'.
   * Defaults to true.
   */
  verify?: boolean
  /** AbortSignal to cancel the loop mid-run. */
  signal?: AbortSignal
  /** Called at the start of each iteration with the current iteration index (0-based). */
  onIterationStart?: (iteration: number, output: OrchestratorOutput) => void
  /** Called when the loop finishes (done or maxIterations). */
  onDone?: (iterations: number, reason: 'done' | 'max-iterations' | 'cancelled') => void
  /** Called when a single iteration errors out (loop continues). */
  onIterationError?: (iteration: number, error: Error) => void
  /** Called after each iteration with the executor's confidence score (if provided). */
  onConfidence?: (iteration: number, confidence: number) => void
  /** Called when the Verification Agent returns a result. */
  onVerification?: (result: VerificationResult) => void
}

export interface AgentLoopResult {
  iterations: number
  reason: 'done' | 'max-iterations' | 'cancelled'
  verification?: VerificationResult
}

// ─── Context builder ──────────────────────────────────────────────────────────

function buildFreshContext(
  timelineEntries: TimelineEntry[],
  sandboxId: string | undefined,
  lastExecutionOutcome?: { method: string; result?: unknown; error?: string },
  /** Appended to the user's goal to inform the executor of missing verification steps. */
  missingHint?: string[],
): OrchestratorContext {
  const state = useWorkspaceStore.getState()
  const base = buildOrchestratorContext(state, { sandboxId, timelineEntries })
  return {
    ...base,
    lastExecution: lastExecutionOutcome
      ? { method: lastExecutionOutcome.method, result: lastExecutionOutcome.result }
      : undefined,
    // Inject missing steps as an explicit hint so the executor can act on them
    ...(missingHint && missingHint.length > 0
      ? { verificationFeedback: missingHint }
      : {}),
  } as OrchestratorContext
}

// ─── Agent loop ───────────────────────────────────────────────────────────────

/**
 * Run the autonomous agent loop with optional verification pass.
 *
 * @example
 * ```ts
 * const result = await runAgentLoop('fix typo in README.md', entries, {
 *   workspaceDispatch: dispatch,
 *   appendTimeline,
 *   sandboxId,
 *   maxIterations: 8,
 *   verify: true,
 *   onVerification: (r) => console.log('verified:', r.verified),
 *   onDone: (n, reason) => console.log('done after', n, 'iterations:', reason),
 * })
 * ```
 */
export async function runAgentLoop(
  userGoal: string,
  timelineEntries: TimelineEntry[],
  opts: AgentLoopOptions,
): Promise<AgentLoopResult> {
  const {
    maxIterations = 10,
    confidenceThreshold = 0.5,
    verify = true,
    signal,
    onIterationStart,
    onDone,
    onIterationError,
    onConfidence,
    onVerification,
    sandboxId,
    ...dispatchOpts
  } = opts

  let iteration = 0
  let lastOutcome: { method: string; result?: unknown; error?: string } | undefined
  let lastOutcomes: ExecutionOutcome[] = []
  let lastOutput: OrchestratorOutput | undefined
  let pendingMissing: string[] = []   // fed back from Verifier to Executor

  while (iteration < maxIterations) {
    // ── Cancellation check ───────────────────────────────────────────────────
    if (signal?.aborted) {
      onDone?.(iteration, 'cancelled')
      return { iterations: iteration, reason: 'cancelled' }
    }

    // ── Build context ────────────────────────────────────────────────────────
    const currentEntries = [...timelineEntries]
    const context = buildFreshContext(currentEntries, sandboxId, lastOutcome, pendingMissing)

    let output: OrchestratorOutput
    try {
      output = await callOrchestratorAI(
        pendingMissing.length > 0
          ? `${userGoal}\n\n[Verification feedback — must address]: ${pendingMissing.join('; ')}`
          : userGoal,
        context,
      )
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err))
      onIterationError?.(iteration, error)
      onDone?.(iteration, 'done')
      return { iterations: iteration, reason: 'done', verification: undefined }
    }

    lastOutput = output
    onIterationStart?.(iteration, output)

    if (output.confidence !== undefined) {
      onConfidence?.(iteration, output.confidence)
    }

    try {
      const result = await dispatchOrchestratorOutput(output, { ...dispatchOpts, sandboxId })
      lastOutcomes = result.outcomes
      const lastNS = [...result.outcomes].reverse().find((o) => !o.skipped)
      if (lastNS) {
        lastOutcome = { method: lastNS.method, result: lastNS.result, error: lastNS.error }
      }
      pendingMissing = []  // clear after successful dispatch
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err))
      onIterationError?.(iteration, error)
    }

    iteration++

    // ── Status + confidence check ────────────────────────────────────────────
    if (!output.status || output.status === 'done') {
      const conf = output.confidence
      if (conf !== undefined && conf < confidenceThreshold) {
        onIterationError?.(
          iteration,
          new Error(`Low confidence (${conf.toFixed(2)} < ${confidenceThreshold}) — continuing`),
        )
        continue
      }

      // ── Verification pass ──────────────────────────────────────────────────
      if (verify && iteration < maxIterations) {
        const wsState  = useWorkspaceStore.getState()
        const panels   = Object.values(wsState.panels ?? {})
        const openTypes = panels.map((p) => p.type)

        const verifyCtx: VerificationContext = {
          userGoal,
          lastOutcomes,
          recentTimeline: [...timelineEntries].slice(-20),
          uiState: { panelCount: panels.length, openTypes },
          executorPlan: lastOutput?.plan ?? [],
          executorConfidence: lastOutput?.confidence,
        }

        let verifyResult: VerificationResult
        try {
          verifyResult = await verifyGoal(verifyCtx)
        } catch (_err) {
          // Verification failure is non-fatal; treat as done.
          onDone?.(iteration, 'done')
          return { iterations: iteration, reason: 'done' }
        }

        onVerification?.(verifyResult)

        if (verifyResult.verified) {
          onDone?.(iteration, 'done')
          return { iterations: iteration, reason: 'done', verification: verifyResult }
        }

        // Not verified — feed missing steps back to Executor
        pendingMissing = verifyResult.missing.length > 0
          ? verifyResult.missing
          : ['Verifier returned unverified with no specific steps; re-check goal']
        continue
      }

      // verify=false or no budget left — trust the executor
      onDone?.(iteration, 'done')
      return { iterations: iteration, reason: 'done' }
    }
    // status === 'continue' → next iteration
  }

  onDone?.(iteration, 'max-iterations')
  return { iterations: iteration, reason: 'max-iterations' }
}
