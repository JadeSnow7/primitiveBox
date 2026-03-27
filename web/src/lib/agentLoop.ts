/**
 * agentLoop.ts
 *
 * Autonomous agent loop: PLAN → ACT → OBSERVE → REPLAN → VERIFY
 *
 * Each iteration:
 *   1. Reads the CURRENT timeline from the store (not an initial snapshot).
 *   2. Calls the Executor AI (LLM or local fallback) → OrchestratorOutput
 *   3. Dispatches output (execution + UI), collecting outcomes.
 *   4. Injects outcomes back into context as `lastExecution`.
 *   5. Checks `status` + confidence threshold:
 *      - 'continue'          → next iteration
 *      - 'done' + low conf   → continue (confidence override)
 *      - 'done' + ok conf    → run Verification Agent
 *        - verified: true    → truly done
 *        - verified: false   → continue with missing[] context
 *   6. maxIterations guard prevents runaway loops.
 *
 * Design:
 *   - Pure async; no React dependency (reads store via getState(), not hooks).
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
import { useOrchestratorStore } from '@/store/orchestratorStore'
import { useWorkspaceStore } from '@/store/workspaceStore'
import { useTimelineStore } from '@/store/timelineStore'
import type { OrchestratorOutput } from '@/types/workspace'
import type { VerificationResult } from '@/types/workspace'

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
  onDone?: (iterations: number, reason: 'done' | 'max-iterations' | 'cancelled' | 'deadlock') => void
  /** Called when a single iteration errors out (loop continues). */
  onIterationError?: (iteration: number, error: Error) => void
  /** Called after each iteration with the executor's confidence score (if provided). */
  onConfidence?: (iteration: number, confidence: number) => void
  /** Called when the Verification Agent returns a result. */
  onVerification?: (result: VerificationResult) => void
  /** Number of identical no-progress iterations before aborting. Defaults to 3. */
  deadlockThreshold?: number
  /** Confidence multiplier applied after each verifier rejection. Defaults to 0.75. */
  verifierConfidenceDecay?: number
  /** After this many verifier rejections, force the local fallback planner. Defaults to 2. */
  localFallbackThreshold?: number
}

export interface AgentLoopResult {
  iterations: number
  reason: 'done' | 'max-iterations' | 'cancelled' | 'deadlock'
  verification?: VerificationResult
}

export class DeadlockError extends Error {
  readonly streak: number
  readonly signature: string

  constructor(message: string, streak: number, signature: string) {
    super(message)
    this.name = 'DeadlockError'
    this.streak = streak
    this.signature = signature
  }
}

// ─── Context builders ─────────────────────────────────────────────────────────

function buildFreshContext(
  sandboxId: string | undefined,
  lastExecutionOutcome?: { method: string; result?: unknown; error?: string },
  /** Appended to the user's goal to inform the executor of missing verification steps. */
  missingHint?: string[],
): OrchestratorContext {
  // Always read the live timeline — never use a stale snapshot from before the loop started.
  const currentEntries = useTimelineStore.getState().entries
  const state = useWorkspaceStore.getState()
  const base = buildOrchestratorContext(state, { sandboxId, timelineEntries: currentEntries })
  return {
    ...base,
    lastExecution: lastExecutionOutcome
      ? {
          method: lastExecutionOutcome.method,
          result: lastExecutionOutcome.result,
          error: lastExecutionOutcome.error,
        }
      : undefined,
    // Inject missing steps as an explicit hint so the executor can act on them
    ...(missingHint && missingHint.length > 0
      ? { verificationFeedback: missingHint }
      : {}),
  } as OrchestratorContext
}

/**
 * Build the VerificationContext from live store state.
 * Extracted as a named helper so future callers cannot accidentally pass a stale snapshot.
 */
function buildVerifierContext(
  userGoal: string,
  lastOutcomes: ExecutionOutcome[],
  lastOutput: OrchestratorOutput | undefined,
): VerificationContext {
  // Read live — must happen AFTER the iteration's dispatch has completed.
  const currentEntries = useTimelineStore.getState().entries
  const wsState = useWorkspaceStore.getState()
  const panels = Object.values(wsState.panels ?? {})

  return {
    userGoal,
    lastOutcomes,
    recentTimeline: currentEntries.slice(-20),
    uiState: { panelCount: panels.length, openTypes: panels.map((p) => p.type) },
    executorPlan: lastOutput?.plan ?? [],
    executorConfidence: lastOutput?.confidence,
    executorStatus: lastOutput?.status,
  }
}

function stableSerialize(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value)
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableSerialize(item)).join(',')}]`
  }
  const entries = Object.entries(value as Record<string, unknown>)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, item]) => `${JSON.stringify(key)}:${stableSerialize(item)}`)
  return `{${entries.join(',')}}`
}

function workspaceFingerprint(): string {
  const state = useWorkspaceStore.getState()
  const panels = Object.values(state.panels ?? {})
    .map((panel) => ({ type: panel.type, props: panel.props }))
    .sort((a, b) => stableSerialize(a).localeCompare(stableSerialize(b)))

  return stableSerialize({
    layout: state.layout,
    panels,
    focusedPanelId: state.focusedPanelId,
  })
}

function buildIterationSignature(
  output: OrchestratorOutput,
  outcomes: ExecutionOutcome[],
  workspaceState: string,
  verifierFeedback: string[],
): string {
  return stableSerialize({
    execution: (output.execution ?? []).map((call) => ({
      method: call.method,
      params: call.params,
    })),
    ui: output.ui ?? [],
    outcomes: outcomes.map((outcome) => ({
      method: outcome.method,
      params: outcome.params,
      error: outcome.error ?? null,
      skipped: outcome.skipped ?? false,
      result: outcome.result ?? null,
    })),
    verifierFeedback,
    workspaceState,
  })
}

// ─── Agent loop ───────────────────────────────────────────────────────────────

/**
 * Run the autonomous agent loop with optional verification pass.
 *
 * Note: `timelineEntries` is NO LONGER a parameter. The loop reads the live
 * store state at the start of each iteration to avoid stale-snapshot bugs.
 *
 * @example
 * ```ts
 * const result = await runAgentLoop('fix typo in README.md', {
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
  opts: AgentLoopOptions,
): Promise<AgentLoopResult> {
  const {
    maxIterations = 10,
    confidenceThreshold = 0.5,
    verify = true,
    deadlockThreshold = 3,
    verifierConfidenceDecay = 0.75,
    localFallbackThreshold = 2,
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
  let verifierRejectStreak = 0
  let deadlockStreak = 0
  let lastIterationSignature: string | undefined
  useOrchestratorStore.getState().setPhase('RUNNING')

  try {
    while (iteration < maxIterations) {
      // ── Cancellation check ─────────────────────────────────────────────────
      if (signal?.aborted) {
        onDone?.(iteration, 'cancelled')
        return { iterations: iteration, reason: 'cancelled' }
      }

      // ── Build context — reads LIVE timeline state ──────────────────────────
      const context = buildFreshContext(sandboxId, lastOutcome, pendingMissing)
      const forceLocalFallback = verifierRejectStreak >= localFallbackThreshold

      let output: OrchestratorOutput
      try {
        output = await callOrchestratorAI(
          [
            userGoal,
            pendingMissing.length > 0
              ? `[Verification feedback — must address]:\n- ${pendingMissing.join('\n- ')}`
              : '',
            forceLocalFallback
              ? '[Safety escalation] Verifier rejected prior attempts repeatedly. Use the conservative local fallback plan and do not repeat the same failing action sequence.'
              : '',
          ].filter(Boolean).join('\n\n'),
          context,
          { forceLocal: forceLocalFallback },
        )
      } catch (err) {
        const error = err instanceof Error ? err : new Error(String(err))
        onIterationError?.(iteration, error)
        onDone?.(iteration, 'done')
        return { iterations: iteration, reason: 'done', verification: undefined }
      }

      const effectiveConfidence = output.confidence !== undefined
        ? Math.max(0, Math.min(1, output.confidence * Math.pow(verifierConfidenceDecay, verifierRejectStreak)))
        : undefined
      const effectiveOutput: OrchestratorOutput = effectiveConfidence === undefined
        ? output
        : { ...output, confidence: effectiveConfidence }

      lastOutput = effectiveOutput
      onIterationStart?.(iteration, effectiveOutput)

      if (effectiveConfidence !== undefined) {
        onConfidence?.(iteration, effectiveConfidence)
      }

      const preDispatchWorkspace = workspaceFingerprint()
      try {
        const result = await dispatchOrchestratorOutput(effectiveOutput, { ...dispatchOpts, sandboxId })
        lastOutcomes = result.outcomes
        const lastNS = [...result.outcomes].reverse().find((o) => !o.skipped)
        if (lastNS) {
          lastOutcome = { method: lastNS.method, result: lastNS.result, error: lastNS.error }
        }
        const failedOutcome = [...result.outcomes].reverse().find((o) => typeof o.error === 'string')
        if (failedOutcome?.error) {
          pendingMissing = [`Execution error: ${failedOutcome.error}`]
        } else {
          pendingMissing = []
        }
      } catch (err) {
        const error = err instanceof Error ? err : new Error(String(err))
        onIterationError?.(iteration, error)
      }

      const postDispatchWorkspace = workspaceFingerprint()
      const iterationSignature = buildIterationSignature(
        effectiveOutput,
        lastOutcomes,
        postDispatchWorkspace,
        pendingMissing,
      )
      if (preDispatchWorkspace === postDispatchWorkspace && iterationSignature === lastIterationSignature) {
        deadlockStreak++
      } else {
        deadlockStreak = 1
        lastIterationSignature = iterationSignature
      }

      if (deadlockStreak >= deadlockThreshold) {
        const error = new DeadlockError(
          `Deadlock guard tripped after ${deadlockStreak} identical no-progress iterations`,
          deadlockStreak,
          iterationSignature,
        )
        onIterationError?.(iteration, error)
        onDone?.(iteration + 1, 'deadlock')
        throw error
      }

      iteration++

      if (pendingMissing.length > 0) {
        continue
      }

      // ── Status + confidence check ──────────────────────────────────────────
      if (!effectiveOutput.status || effectiveOutput.status === 'done') {
        const conf = effectiveOutput.confidence
        if (conf !== undefined && conf < confidenceThreshold) {
          onIterationError?.(
            iteration,
            new Error(`Low confidence (${conf.toFixed(2)} < ${confidenceThreshold}) — continuing`),
          )
          continue
        }

        // ── Verification pass — reads LIVE timeline state ────────────────────
        if (verify && iteration < maxIterations) {
          const verifyCtx = buildVerifierContext(userGoal, lastOutcomes, lastOutput)

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
            verifierRejectStreak = 0
            onDone?.(iteration, 'done')
            return { iterations: iteration, reason: 'done', verification: verifyResult }
          }

          verifierRejectStreak++

          // Not verified — feed missing steps back to Executor
          const feedback: string[] = []
          if (verifyResult.missing.length > 0) {
            feedback.push(`Missing: ${verifyResult.missing.join(', ')}`)
          }
          if (verifyResult.recommendedNext?.length > 0) {
            feedback.push(`Recommended: ${verifyResult.recommendedNext.join(', ')}`)
          }
          if (feedback.length === 0) {
            feedback.push('Verifier returned unverified with no specific steps; re-check goal')
          }

          pendingMissing = feedback
          continue
        }

        // verify=false or no budget left — trust the executor
        onDone?.(iteration, 'done')
        return { iterations: iteration, reason: 'done' }
      }
      // status === 'continue' → next iteration
    }
  } finally {
    useOrchestratorStore.getState().setPhase('IDLE')
  }

  onDone?.(iteration, 'max-iterations')
  return { iterations: iteration, reason: 'max-iterations' }
}
