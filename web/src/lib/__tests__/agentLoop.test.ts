/**
 * agentLoop.test.ts
 *
 * Verifies the critical invariant: even with an empty timeline at loop start,
 * the Verification Agent's context contains execution.result entries written
 * during the first iteration — proving the loop reads LIVE store state, not
 * the stale initial snapshot.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useOrchestratorStore } from '@/store/orchestratorStore'
import { useTimelineStore } from '@/store/timelineStore'
import type { TimelineState } from '@/store/timelineStore'
import type { VerificationContext } from '@/lib/verificationAgent'
import { callOrchestratorAI } from '@/api/uiPrimitives'
import { dispatchOrchestratorOutput } from '@/lib/orchestratorDispatcher'

// ─── Mocks ────────────────────────────────────────────────────────────────────

// Orchestrator AI: returns status:'done' immediately
vi.mock('@/api/uiPrimitives', () => ({
  callOrchestratorAI: vi.fn().mockResolvedValue({
    groupId: 'test-group',
    status: 'done',
    confidence: 0.9,
    plan: [],
    execution: [],
    ui: [],
  }),
  buildOrchestratorContext: vi.fn(() => ({})),
}))

// Dispatcher: appends a fake execution.result to the live store each call
vi.mock('@/lib/orchestratorDispatcher', () => ({
  dispatchOrchestratorOutput: vi.fn(async (
    _output: unknown,
    opts: { appendTimeline: TimelineState['append'] },
  ) => {
    opts.appendTimeline({
      kind: 'execution.result',
      groupId: 'test-group',
      correlationId: 'cid-mock',
      method: 'fs.read',
      result: { content: 'mock-result' },
    })
    return { outcomes: [{ method: 'fs.read', params: {}, result: { content: 'mock-result' } }] }
  }),
}))

// Capture the verifyGoal call context for assertion
let capturedVerifyCtx: VerificationContext | undefined

vi.mock('@/lib/verificationAgent', () => ({
  verifyGoal: vi.fn((ctx: VerificationContext) => {
    capturedVerifyCtx = ctx
    return Promise.resolve({ verified: true, confidence: 0.9, reason: 'ok', missing: [] })
  }),
}))

vi.mock('@/store/workspaceStore', () => ({
  useWorkspaceStore: {
    getState: vi.fn(() => ({ panels: {}, layout: { type: 'empty' } })),
  },
}))

// ─── Tests ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  useTimelineStore.getState().clear()
  useOrchestratorStore.getState().reset()
  capturedVerifyCtx = undefined
  vi.clearAllMocks()
  vi.mocked(callOrchestratorAI).mockResolvedValue({
    groupId: 'test-group',
    status: 'done',
    confidence: 0.9,
    plan: [],
    execution: [],
    ui: [],
  })
  vi.mocked(dispatchOrchestratorOutput).mockImplementation(async (
    _output: unknown,
    opts: { appendTimeline: TimelineState['append'] },
  ) => {
    opts.appendTimeline({
      kind: 'execution.result',
      groupId: 'test-group',
      correlationId: 'cid-mock',
      method: 'fs.read',
      result: { content: 'mock-result' },
    })
    return { outcomes: [{ method: 'fs.read', params: {}, result: { content: 'mock-result' } }] }
  })
})

describe('runAgentLoop — live timeline read', () => {
  it('verifier context contains execution.result entries written during iteration 1, even with empty initial timeline', async () => {
    // Import after mocks are set up
    const { runAgentLoop } = await import('@/lib/agentLoop')

    // Timeline is empty before loop starts
    expect(useTimelineStore.getState().entries).toHaveLength(0)

    await runAgentLoop('test goal', {
      workspaceDispatch: vi.fn(),
      appendTimeline: useTimelineStore.getState().append,
      sandboxId: undefined,
      maxIterations: 2,   // must be >1 so the verify budget check passes at iteration=1
      verify: true,
    })

    // After loop, the verifier should have seen the execution.result
    // that was appended during the loop's dispatch phase.
    expect(capturedVerifyCtx).toBeDefined()
    const resultEntries = capturedVerifyCtx!.recentTimeline.filter(
      (e) => e.kind === 'execution.result',
    )
    expect(resultEntries.length).toBeGreaterThan(0)
  })

  it('aborts after 3 identical no-progress failing iterations', async () => {
    const { runAgentLoop, DeadlockError } = await import('@/lib/agentLoop')
    const mockedCallOrchestratorAI = vi.mocked(callOrchestratorAI)
    const mockedDispatch = vi.mocked(dispatchOrchestratorOutput)

    mockedCallOrchestratorAI.mockResolvedValue({
      groupId: 'deadlock-group',
      status: 'continue',
      confidence: 0.9,
      execution: [{ id: 'same-call', method: 'fs.read', params: { path: 'README.md' } }],
      ui: [],
    })
    mockedDispatch.mockResolvedValue({
      outcomes: [{ method: 'fs.read', params: { path: 'README.md' }, error: 'ENOENT' }],
    })

    await expect(runAgentLoop('stuck goal', {
      workspaceDispatch: vi.fn(),
      appendTimeline: useTimelineStore.getState().append,
      sandboxId: 'sb-1',
      verify: false,
      maxIterations: 5,
      deadlockThreshold: 3,
    })).rejects.toBeInstanceOf(DeadlockError)

    expect(mockedCallOrchestratorAI).toHaveBeenCalledTimes(3)
  })

  it('feeds reviewer rejection back into the next planning iteration', async () => {
    const { runAgentLoop } = await import('@/lib/agentLoop')
    const mockedCallOrchestratorAI = vi.mocked(callOrchestratorAI)
    const mockedDispatch = vi.mocked(dispatchOrchestratorOutput)

    mockedCallOrchestratorAI
      .mockResolvedValueOnce({
        groupId: 'review-1',
        status: 'done',
        confidence: 0.9,
        execution: [{ id: 'email-1', method: 'email.send', params: { to: 'alice@example.com' } }],
        ui: [],
      })
      .mockResolvedValueOnce({
        groupId: 'review-2',
        status: 'done',
        confidence: 0.9,
        execution: [],
        ui: [],
      })

    mockedDispatch
      .mockResolvedValueOnce({
        outcomes: [{
          method: 'email.send',
          params: { to: 'alice@example.com' },
          error: 'Execution completely REJECTED by Human Reviewer. Re-evaluate your plan.',
        }],
      })
      .mockResolvedValueOnce({
        outcomes: [],
      })

    await runAgentLoop('send a risky email', {
      workspaceDispatch: vi.fn(),
      appendTimeline: useTimelineStore.getState().append,
      sandboxId: 'sb-1',
      verify: false,
      maxIterations: 3,
    })

    expect(mockedCallOrchestratorAI).toHaveBeenCalledTimes(2)
    const secondPrompt = mockedCallOrchestratorAI.mock.calls[1]?.[0]
    expect(secondPrompt).toContain('Execution error: Execution completely REJECTED by Human Reviewer')
  })

  it('returns cancelled without dispatching when started with an aborted signal', async () => {
    const { runAgentLoop } = await import('@/lib/agentLoop')
    const abort = new AbortController()
    abort.abort()

    const result = await runAgentLoop('cancelled goal', {
      workspaceDispatch: vi.fn(),
      appendTimeline: useTimelineStore.getState().append,
      sandboxId: 'sb-1',
      verify: false,
      signal: abort.signal,
    })

    expect(result.reason).toBe('cancelled')
    expect(vi.mocked(callOrchestratorAI)).not.toHaveBeenCalled()
    expect(vi.mocked(dispatchOrchestratorOutput)).not.toHaveBeenCalled()
  })
})
