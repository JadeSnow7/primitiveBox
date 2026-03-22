/**
 * verificationAgent.ts
 *
 * Calls the Verification Agent (LLM or local fallback) to assess whether the
 * executor's goal is truly achieved.
 *
 * Called by agentLoop.ts after the Executor reports status: 'done'.
 * If verified: false, the loop feeds `missing[]` back to the Executor as
 * additional context for the next iteration.
 *
 * Architecture note:
 *   Executor Agent  →  status: 'done'  →  Verification Agent
 *       ↑                                        │
 *       └──────── verified: false / missing[] ───┘
 *                         ↓
 *              verified: true → truly done
 */

import { VERIFICATION_SYSTEM_PROMPT } from '@/lib/verificationSystemPrompt'
import type { VerificationResult } from '@/types/workspace'
import type { ExecutionOutcome } from '@/lib/orchestratorDispatcher'
import type { TimelineEntry } from '@/types/timeline'

// ─── Context ──────────────────────────────────────────────────────────────────

export interface VerificationContext {
  userGoal: string
  /** Most recent outcomes from the Executor's last iteration. */
  lastOutcomes: ExecutionOutcome[]
  /** Recent timeline entries (last 20 max) for context. */
  recentTimeline: TimelineEntry[]
  /** Panel count and open panel types for UI state. */
  uiState: {
    panelCount: number
    openTypes: string[]
  }
  /** The Executor's final plan steps (for cross-checking). */
  executorPlan: Array<{ step: string; reason: string }>
  /** The Executor's own confidence score (0–1). */
  executorConfidence?: number
}

// ─── Local fallback ───────────────────────────────────────────────────────────

/**
 * Conservative local verifier: used when no LLM endpoint is configured.
 * Checks for structural evidence rather than semantic understanding.
 */
function localVerify(goal: string, ctx: VerificationContext): VerificationResult {
  const { lastOutcomes, recentTimeline } = ctx
  const missing: string[] = []

  const hasResults = lastOutcomes.some((o) => !o.skipped && o.result !== undefined)
  const hasErrors  = lastOutcomes.some((o) => o.error !== undefined)
  const kindsSeen  = new Set(recentTimeline.map((e) => e.kind))

  if (!hasResults) {
    missing.push('No execution results found — goal may not have been acted upon')
  }
  if (hasErrors) {
    missing.push('One or more execution steps produced errors')
  }

  // Heuristic: if goal mentions file modification, look for fs.write or fs.diff evidence
  const goalLower = goal.toLowerCase()
  const needsWrite = /edit|fix|write|modify|update|change/i.test(goalLower)
  const hasWrite = lastOutcomes.some((o) => o.method === 'fs.write' && !o.skipped)
  const hasDiff  = lastOutcomes.some((o) => o.method === 'fs.diff')
  if (needsWrite && !hasWrite) {
    missing.push('Goal implies a file modification but no fs.write was executed')
  }
  if (needsWrite && hasWrite && !hasDiff) {
    missing.push('Change was made but not verified with fs.diff')
  }

  // Heuristic: if goal is a search/read, any fs.read or code.search is evidence
  const needsRead = /read|show|list|find|search/i.test(goalLower)
  const hasRead   = lastOutcomes.some((o) =>
    ['fs.read', 'fs.list', 'code.search'].includes(o.method) && !o.skipped,
  )
  if (needsRead && !hasRead) {
    missing.push('Goal implies reading or searching but no such execution found')
  }

  // Check that timeline has moved beyond just 'plan'
  const hasExecutionResult = kindsSeen.has('execution.result')
  if (!hasExecutionResult) {
    missing.push('No execution.result in timeline — actions may not have been performed')
  }

  const verified = missing.length === 0
  const confidence = verified
    ? (ctx.executorConfidence ?? 0.7) * 0.9  // slightly discount vs executor
    : Math.max(0.1, (ctx.executorConfidence ?? 0.5) * 0.4)

  return {
    verified,
    confidence: Math.round(confidence * 100) / 100,
    reason: verified
      ? `Local verifier found structural evidence: ${hasResults ? 'execution results present' : ''}, ${hasDiff ? 'diff verified' : ''}`.trim()
      : `Local verifier found issues: ${missing.join('; ')}`,
    missing,
  }
}

// ─── LLM verifier ─────────────────────────────────────────────────────────────

async function llmVerify(
  ctx: VerificationContext,
  endpoint: string,
  apiKey: string,
  model: string,
): Promise<VerificationResult> {
  const userMessage = JSON.stringify({
    userGoal: ctx.userGoal,
    lastExecutionResults: ctx.lastOutcomes.map((o) => ({
      method: o.method,
      result: o.result,
      error: o.error,
      skipped: o.skipped,
    })),
    uiState: ctx.uiState,
    recentTimeline: ctx.recentTimeline.slice(-20).map((e) => ({
      kind: e.kind,
      method: 'method' in e ? e.method : undefined,
    })),
    executorPlan: ctx.executorPlan,
    executorConfidence: ctx.executorConfidence,
  })

  const res = await fetch(`${endpoint}/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
    },
    body: JSON.stringify({
      model,
      messages: [
        { role: 'system', content: VERIFICATION_SYSTEM_PROMPT },
        { role: 'user', content: userMessage },
      ],
      temperature: 0,
      max_tokens: 512,
    }),
  })

  if (!res.ok) {
    throw new Error(`Verification LLM error ${res.status}: ${await res.text()}`)
  }

  const data = (await res.json()) as { choices: Array<{ message: { content: string } }> }
  const content = data.choices?.[0]?.message?.content?.trim() ?? ''

  // Strip code fences if model wraps output
  const clean = content
    .replace(/^```(?:json)?\s*/i, '')
    .replace(/\s*```$/i, '')
    .trim()

  const parsed = JSON.parse(clean) as unknown
  return validateVerificationResult(parsed)
}

// ─── Validator ────────────────────────────────────────────────────────────────

function validateVerificationResult(raw: unknown): VerificationResult {
  if (typeof raw !== 'object' || raw === null) {
    throw new Error('Verification result is not an object')
  }
  const o = raw as Record<string, unknown>
  if (typeof o['verified'] !== 'boolean') throw new Error('verified must be boolean')
  if (typeof o['confidence'] !== 'number') throw new Error('confidence must be number')
  if (typeof o['reason'] !== 'string') throw new Error('reason must be string')
  if (!Array.isArray(o['missing'])) throw new Error('missing must be array')
  return {
    verified: o['verified'],
    confidence: Math.min(1, Math.max(0, o['confidence'])),
    reason: o['reason'],
    missing: (o['missing'] as unknown[]).filter((m): m is string => typeof m === 'string'),
  }
}

// ─── Public API ───────────────────────────────────────────────────────────────

/**
 * Verify whether the executor's goal is truly achieved.
 * Falls back to local heuristics when no LLM is configured.
 */
export async function verifyGoal(ctx: VerificationContext): Promise<VerificationResult> {
  const endpoint = import.meta.env['VITE_ORCHESTRATOR_URL'] as string | undefined
  const apiKey   = import.meta.env['VITE_ORCHESTRATOR_KEY'] as string | undefined
  const model    = (import.meta.env['VITE_ORCHESTRATOR_MODEL'] as string | undefined) ?? 'gpt-4o-mini'

  if (!endpoint) {
    return localVerify(ctx.userGoal, ctx)
  }

  try {
    return await llmVerify(ctx, endpoint, apiKey ?? '', model)
  } catch (err) {
    console.warn('[verificationAgent] LLM call failed, using local fallback:', err)
    return localVerify(ctx.userGoal, ctx)
  }
}
