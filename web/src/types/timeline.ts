// ─── Timeline entry types ─────────────────────────────────────────────────────
//
// Every orchestrator invocation produces one groupId. All entries produced by
// that invocation share that groupId to support causal inspection and replay.

import type { PlanStep } from '@/types/workspace'

export type TimelineEntry =
  /** The orchestrator's reasoning plan for this invocation */
  | {
      kind: 'plan'
      groupId: string
      id: string
      ts: string
      steps: PlanStep[]
    }
  /** The orchestrator intends to call an execution primitive */
  | {
      kind: 'execution.call'
      groupId: string
      id: string        // same id as ExecutionCall.id
      ts: string
      method: string
      params: unknown
    }
  /** Execution primitive returned a result */
  | {
      kind: 'execution.result'
      groupId: string
      id: string        // matches the execution.call id
      ts: string
      result: unknown
    }
  /** Execution was skipped (e.g. no active sandbox id) */
  | {
      kind: 'execution.skipped'
      groupId: string
      id: string
      ts: string
      reason: string
    }
  /**
   * Execution was SIMULATED during replay (safe stub — no real side effect).
   * Carries the original method + params so the timeline stays semantically complete.
   */
  | {
      kind: 'execution.simulated'
      groupId: string
      id: string
      ts: string
      method: string
      params: unknown
    }
  /** A UI primitive was dispatched to the workspace */
  | {
      kind: 'ui'
      groupId: string
      id: string
      ts: string
      method: string
      params: unknown
    }
