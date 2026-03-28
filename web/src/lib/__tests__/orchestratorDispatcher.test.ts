/**
 * orchestratorDispatcher.test.ts
 *
 * Verifies that:
 *   - without sandboxId → all execution calls produce execution.skipped with reason:'no_sandbox'
 *   - with sandboxId + successful callPrimitive → produces execution.result with correct correlationId
 *   - execution.result correlationId matches the preceding execution.call correlationId
 *   - high-risk groups auto-create a checkpoint before mutation
 *   - state.restore resolves to the latest known checkpoint when omitted
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { dispatchOrchestratorOutput } from '@/lib/orchestratorDispatcher'
import { useOrchestratorStore } from '@/store/orchestratorStore'
import { usePrimitiveStore } from '@/store/primitiveStore'
import { useTimelineStore } from '@/store/timelineStore'
import { upsertWorkspaceEntities } from '@/store/workspaceStore'
import type { OrchestratorOutput } from '@/types/workspace'
import type { TimelineEntry } from '@/types/timeline'
import type { PrimitiveSchema } from '@/types/primitive'

// Mock callPrimitive
vi.mock('@/api/primitives', () => ({
  callPrimitive: vi.fn(),
}))

// Mock mapExecutionResultToUI to return empty (UI side effects not under test here)
vi.mock('@/lib/executionMapper', () => ({
  mapExecutionResultToUI: vi.fn(() => []),
}))

// Mock getWorkspacePanels
vi.mock('@/store/workspaceStore', () => ({
  getWorkspacePanels: vi.fn(() => ({})),
  upsertWorkspaceEntities: vi.fn(),
}))

import { callPrimitive } from '@/api/primitives'

const mockedCallPrimitive = callPrimitive as ReturnType<typeof vi.fn>
const mockedUpsertWorkspaceEntities = upsertWorkspaceEntities as ReturnType<typeof vi.fn>

function makeOutput(callId: string, method = 'fs.read'): OrchestratorOutput {
  return {
    groupId: 'test-group',
    execution: [{ id: callId, method: method as 'fs.read', params: { path: '/foo' } }],
  }
}

beforeEach(() => {
  useTimelineStore.getState().clear()
  useOrchestratorStore.getState().reset()
  const primitives: PrimitiveSchema[] = [
    {
      name: 'fs.read',
      description: 'Read a file',
      kind: 'system',
      input_schema: {},
      output_schema: {},
      intent: { category: 'query', side_effect: 'read', reversible: true, risk_level: 'low' },
    },
    {
      name: 'state.checkpoint',
      description: 'Create a checkpoint',
      kind: 'system',
      input_schema: {},
      output_schema: {},
      intent: { category: 'mutation', side_effect: 'write', reversible: true, risk_level: 'low' },
    },
    {
      name: 'state.restore',
      description: 'Restore a checkpoint',
      kind: 'system',
      input_schema: {},
      output_schema: {},
      intent: { category: 'rollback', side_effect: 'write', reversible: true, risk_level: 'high' },
    },
    {
      name: 'verify.test',
      description: 'Run tests',
      kind: 'system',
      input_schema: {},
      output_schema: {},
      intent: { category: 'verification', side_effect: 'exec', reversible: true, risk_level: 'medium' },
    },
    {
      name: 'db.execute',
      description: 'Mutate database',
      kind: 'system',
      input_schema: {},
      output_schema: {},
      intent: { category: 'mutation', side_effect: 'exec', reversible: false, risk_level: 'high' },
    },
    {
      name: 'email.send',
      description: 'Send email',
      kind: 'app',
      input_schema: {},
      output_schema: {},
      intent: { category: 'mutation', side_effect: 'external', reversible: false, risk_level: 'high' },
    },
    {
      name: 'data.insert',
      description: 'Insert a row into a table',
      kind: 'app',
      input_schema: {},
      output_schema: {},
      intent: { category: 'mutation', side_effect: 'write', reversible: false, risk_level: 'high' },
    },
    {
      name: 'data.query',
      description: 'Execute a SELECT statement',
      kind: 'app',
      input_schema: {},
      output_schema: {},
      intent: { category: 'query', side_effect: 'read', reversible: true, risk_level: 'low' },
    },
  ]
  usePrimitiveStore.setState({
    status: 'ready',
    error: null,
    primitives,
    primitivesByName: Object.fromEntries(primitives.map((primitive) => [primitive.name, primitive])),
  })
  vi.clearAllMocks()
})

describe('dispatchOrchestratorOutput', () => {
  it('produces execution.skipped with reason no_sandbox when sandboxId is absent', async () => {
    const appendTimeline = useTimelineStore.getState().append

    await dispatchOrchestratorOutput(makeOutput('call-1'), {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: undefined,
    })

    const entries = useTimelineStore.getState().entries
    const skipped = entries.filter((e) => e.kind === 'execution.skipped')
    expect(skipped).toHaveLength(1)
    const s = skipped[0] as Extract<TimelineEntry, { kind: 'execution.skipped' }>
    expect(s.reason).toBe('no_sandbox')
    expect(s.correlationId).toBe('call-1')
    expect(s.method).toBe('fs.read')
  })

  it('produces execution.result with matching correlationId when sandboxId is provided', async () => {
    mockedCallPrimitive.mockResolvedValue({ content: 'file-content' })

    const appendTimeline = useTimelineStore.getState().append

    const result = await dispatchOrchestratorOutput(makeOutput('call-42'), {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-123',
    })

    expect(result.outcomes).toHaveLength(1)
    expect(result.outcomes[0].skipped).toBeUndefined()
    expect(result.outcomes[0].result).toEqual({ content: 'file-content' })

    const entries = useTimelineStore.getState().entries
    const callEntry  = entries.find((e) => e.kind === 'execution.call') as Extract<TimelineEntry, { kind: 'execution.call' }>
    const resultEntry = entries.find((e) => e.kind === 'execution.result') as Extract<TimelineEntry, { kind: 'execution.result' }>

    expect(callEntry).toBeDefined()
    expect(resultEntry).toBeDefined()
    expect(callEntry.correlationId).toBe('call-42')
    expect(resultEntry.correlationId).toBe('call-42')
    expect(resultEntry.entityIds).toEqual(['file:/foo'])
    expect(mockedUpsertWorkspaceEntities).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          id: 'file:/foo',
          type: 'file',
        }),
      ]),
    )
    // Both entries get different event ids (auto-generated)
    expect(callEntry.id).not.toBe(resultEntry.id)
  })

  it('outcome.skipped is true when sandboxId is absent', async () => {
    const appendTimeline = useTimelineStore.getState().append

    const { outcomes } = await dispatchOrchestratorOutput(makeOutput('call-skip'), {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: undefined,
    })

    expect(outcomes[0].skipped).toBe(true)
  })

  it('auto-inserts state.checkpoint before high-risk execution groups', async () => {
    mockedCallPrimitive
      .mockResolvedValueOnce({ checkpoint_id: 'ckpt-auto-1', timestamp: '2026-03-23T00:00:00Z' })
      .mockResolvedValueOnce({ ok: true })

    const appendTimeline = useTimelineStore.getState().append

    await dispatchOrchestratorOutput({
      groupId: 'risk-group',
      execution: [{ id: 'write-1', method: 'verify.test', params: {} }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-123',
    })

    expect(mockedCallPrimitive).toHaveBeenNthCalledWith(1, 'sb-123', 'state.checkpoint', {
      label: 'auto:risk-group',
    })
    expect(mockedCallPrimitive).toHaveBeenNthCalledWith(2, 'sb-123', 'verify.test', {})

    const entries = useTimelineStore.getState().entries
    const callEntries = entries.filter((e) => e.kind === 'execution.call') as Array<
      Extract<TimelineEntry, { kind: 'execution.call' }>
    >
    expect(callEntries[0].method).toBe('state.checkpoint')
    expect(callEntries[1].method).toBe('verify.test')
  })

  it('fills state.restore params with the latest checkpoint id when omitted', async () => {
    const appendTimeline = useTimelineStore.getState().append
    appendTimeline({
      kind: 'execution.result',
      groupId: 'previous-group',
      correlationId: 'checkpoint-call',
      method: 'state.checkpoint',
      result: { checkpoint_id: 'ckpt-latest', timestamp: '2026-03-23T00:00:00Z' },
    })
    mockedCallPrimitive.mockResolvedValue({ restored_to: 'ckpt-latest', files_changed: 1 })

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'restore-group',
      execution: [{ id: 'restore-1', method: 'state.restore', params: {} }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-restore',
    })

    useOrchestratorStore.getState().approvePendingReview()
    await pendingDispatch

    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-restore', 'state.restore', {
      checkpoint_id: 'ckpt-latest',
    })
  })

  it('pauses high-risk execution for review and does not call sandbox before approval', async () => {
    mockedCallPrimitive.mockResolvedValue({ delivered: true })
    const appendTimeline = useTimelineStore.getState().append

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'review-group',
      execution: [{ id: 'email-1', method: 'email.send', params: { to: 'alice@example.com', body: 'hello' } }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-review',
    })

    expect(useOrchestratorStore.getState().phase).toBe('AWAITING_REVIEW')
    expect(mockedCallPrimitive).not.toHaveBeenCalled()

    const entries = useTimelineStore.getState().entries
    const pendingEntry = entries.find((e) => e.kind === 'execution.pending_review') as Extract<
      TimelineEntry,
      { kind: 'execution.pending_review' }
    >
    expect(pendingEntry.method).toBe('email.send')
    expect(pendingEntry.reversible).toBe(false)
    expect(pendingEntry.risk_level).toBe('high')

    useOrchestratorStore.getState().approvePendingReview()
    await pendingDispatch

    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-review', 'email.send', {
      to: 'alice@example.com',
      body: 'hello',
    })
  })

  it('pauses db.execute for review before sandbox execution', async () => {
    mockedCallPrimitive
      .mockResolvedValueOnce({ checkpoint_id: 'ckpt-db', timestamp: '2026-03-24T00:00:00Z' })
      .mockResolvedValueOnce({ rows_affected: 1 })
    const appendTimeline = useTimelineStore.getState().append

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'db-review-group',
      execution: [{
        id: 'db-exec-1',
        method: 'db.execute',
        params: { connection: { dialect: 'sqlite', path: 'sample.db' }, query: 'DROP TABLE widgets' },
      }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-review',
    })

    await Promise.resolve()
    expect(useOrchestratorStore.getState().phase).toBe('AWAITING_REVIEW')
    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-review', 'state.checkpoint', {
      label: 'auto:db-review-group',
    })

    useOrchestratorStore.getState().approvePendingReview()
    await pendingDispatch

    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-review', 'db.execute', {
      connection: { dialect: 'sqlite', path: 'sample.db' },
      query: 'DROP TABLE widgets',
    })
  })

  it('records execution.rejected and returns synthetic feedback when reviewer rejects', async () => {
    const appendTimeline = useTimelineStore.getState().append

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'review-reject-group',
      execution: [{ id: 'email-2', method: 'email.send', params: { to: 'bob@example.com', subject: 'hi' } }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-review',
    })

    expect(mockedCallPrimitive).not.toHaveBeenCalled()
    useOrchestratorStore.getState().rejectPendingReview()

    const result = await pendingDispatch
    expect(mockedCallPrimitive).not.toHaveBeenCalled()
    expect(result.outcomes[0].error).toContain('REJECTED by Human Reviewer')

    const rejectedEntry = useTimelineStore.getState().entries.find(
      (e) => e.kind === 'execution.rejected',
    ) as Extract<TimelineEntry, { kind: 'execution.rejected' }>
    expect(rejectedEntry.method).toBe('email.send')
    expect(rejectedEntry.decision).toBe('rejected')
  })

  it('fails closed immediately when review signal is already aborted', async () => {
    const appendTimeline = useTimelineStore.getState().append
    const abort = new AbortController()
    abort.abort()

    const result = await dispatchOrchestratorOutput({
      groupId: 'review-aborted-group',
      execution: [{ id: 'email-aborted', method: 'email.send', params: { to: 'carol@example.com' } }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-review',
      signal: abort.signal,
    })

    expect(mockedCallPrimitive).not.toHaveBeenCalled()
    expect(result.outcomes[0].error).toContain('REJECTED by Human Reviewer')
    expect(useOrchestratorStore.getState().phase).not.toBe('AWAITING_REVIEW')

    const rejectedEntry = useTimelineStore.getState().entries.find(
      (e) => e.kind === 'execution.rejected',
    ) as Extract<TimelineEntry, { kind: 'execution.rejected' }>
    expect(rejectedEntry.method).toBe('email.send')
    expect(rejectedEntry.decision).toBe('rejected')
  })

  it('fails closed when review is aborted after entering pending state', async () => {
    const appendTimeline = useTimelineStore.getState().append
    const abort = new AbortController()

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'review-abort-during-pending-group',
      execution: [{ id: 'email-abort-pending', method: 'email.send', params: { to: 'dave@example.com' } }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-review',
      signal: abort.signal,
    })

    await Promise.resolve()
    expect(useOrchestratorStore.getState().phase).toBe('AWAITING_REVIEW')

    abort.abort()
    const result = await pendingDispatch

    expect(mockedCallPrimitive).not.toHaveBeenCalled()
    expect(result.outcomes[0].error).toContain('REJECTED by Human Reviewer')
    expect(useOrchestratorStore.getState().phase).not.toBe('AWAITING_REVIEW')
  })

  it('pauses data.insert (app primitive) for review — approves and dispatches exact payload', async () => {
    mockedCallPrimitive.mockResolvedValue({ inserted: true, last_insert_id: 7, rows_affected: 1 })
    const appendTimeline = useTimelineStore.getState().append

    const insertParams = { table: 'products', values: { name: 'Smoke Widget', price: 1.99 } }
    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'app-review-group',
      execution: [{
        id: 'insert-1',
        method: 'data.insert' as 'fs.read', // app primitive; cast to satisfy TS; validated at runtime
        params: insertParams,
      }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-data',
    })

    // Loop yields at the review gate before calling the sandbox
    await Promise.resolve()
    expect(useOrchestratorStore.getState().phase).toBe('AWAITING_REVIEW')

    const pending = useOrchestratorStore.getState().pendingReview
    expect(pending?.method).toBe('data.insert')
    expect(pending?.params).toEqual(insertParams)
    expect(pending?.intent.risk_level).toBe('high')
    expect(pending?.intent.reversible).toBe(false)

    // Verify the sandbox was NOT called before approval — payload is unforgeable
    expect(mockedCallPrimitive).not.toHaveBeenCalledWith('sb-data', 'data.insert', expect.anything())

    // Human approves — exact original payload must be dispatched
    useOrchestratorStore.getState().approvePendingReview()
    const result = await pendingDispatch

    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-data', 'data.insert', insertParams)
    expect(result.outcomes[0].result).toEqual({ inserted: true, last_insert_id: 7, rows_affected: 1 })

    const pendingEntry = useTimelineStore.getState().entries.find(
      (e) => e.kind === 'execution.pending_review',
    ) as Extract<TimelineEntry, { kind: 'execution.pending_review' }>
    expect(pendingEntry.method).toBe('data.insert')
    expect(pendingEntry.reversible).toBe(false)
    expect(pendingEntry.risk_level).toBe('high')
  })

  it('data.query (low-risk app primitive) dispatches without review', async () => {
    mockedCallPrimitive.mockResolvedValue({ columns: ['id', 'name'], rows: [[1, 'Widget']], row_count: 1 })
    const appendTimeline = useTimelineStore.getState().append

    const pendingDispatch = dispatchOrchestratorOutput({
      groupId: 'app-query-group',
      execution: [{
        id: 'query-1',
        method: 'data.query' as 'fs.read',
        params: { sql: 'SELECT id, name FROM products' },
      }],
    }, {
      workspaceDispatch: vi.fn(),
      appendTimeline,
      sandboxId: 'sb-data',
    })

    // No review gate — phase stays RUNNING (not AWAITING_REVIEW)
    await Promise.resolve()
    expect(useOrchestratorStore.getState().phase).not.toBe('AWAITING_REVIEW')

    const result = await pendingDispatch
    expect(result.outcomes[0].result).toEqual({
      columns: ['id', 'name'],
      rows: [[1, 'Widget']],
      row_count: 1,
    })
    expect(mockedCallPrimitive).toHaveBeenCalledWith('sb-data', 'data.query', {
      sql: 'SELECT id, name FROM products',
    })
  })

  it('fails closed when the primitive catalog is unavailable', async () => {
    usePrimitiveStore.setState({
      status: 'error',
      error: 'catalog fetch failed',
      primitives: [],
      primitivesByName: {},
    })

    await expect(dispatchOrchestratorOutput(makeOutput('call-catalog'), {
      workspaceDispatch: vi.fn(),
      appendTimeline: useTimelineStore.getState().append,
      sandboxId: 'sb-123',
    })).rejects.toThrow('Primitive catalog unavailable')

    expect(mockedCallPrimitive).not.toHaveBeenCalled()
    const skippedEntry = useTimelineStore.getState().entries.find(
      (entry) => entry.kind === 'execution.skipped',
    ) as Extract<TimelineEntry, { kind: 'execution.skipped' }>
    expect(skippedEntry.reason).toBe('validation_failed')
  })
})
