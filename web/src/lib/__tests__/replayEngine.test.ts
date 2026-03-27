/**
 * replayEngine.test.ts
 *
 * Verifies that:
 *   - simulate mode correctly matches execution.call↔execution.result by correlationId
 *   - two call/result pairs in the same group are both dispatched in order
 *   - mismatched correlationId prevents dispatch for that result entry
 *   - simulate mode still replays groups that have no checkpoint
 */

import { describe, it, expect, vi } from 'vitest'
import { replayTimelineGroup } from '@/lib/replayEngine'
import type { TimelineEntry } from '@/types/timeline'

vi.mock('@/api/primitives', () => ({
  callPrimitive: vi.fn(),
}))

// Mock mapExecutionResultToUI to return one UI primitive per call
vi.mock('@/lib/executionMapper', () => ({
  mapExecutionResultToUI: vi.fn((method: string, _params: unknown, _result: unknown, correlationId: string) => [
    { method: 'ui.panel.open', params: { type: 'primitive', props: { sourceExecutionId: correlationId, method } } },
  ]),
}))

function makeEntries(pairs: Array<{ correlationId: string; method?: string; result?: unknown }>): TimelineEntry[] {
  const now = new Date().toISOString()
  const entries: TimelineEntry[] = []
  pairs.forEach(({ correlationId, method = 'fs.read', result = { content: 'ok' } }, i) => {
    entries.push({
      kind: 'execution.call',
      groupId: 'g1',
      id: `call-evt-${i}`,
      ts: now,
      correlationId,
      method,
      params: { path: '/test' },
    })
    entries.push({
      kind: 'execution.result',
      groupId: 'g1',
      id: `result-evt-${i}`,
      ts: now,
      correlationId,
      method,
      result,
    })
  })
  return entries
}

describe('replayEngine (simulate mode)', () => {
  it('dispatches once for a single matched call/result pair after restore', async () => {
    const workspaceDispatch = vi.fn()
    const dispatched = await replayTimelineGroup({
      groupId: 'g1',
      mode: 'simulate',
      sandboxId: 'sb-1',
      entries: [
        {
          kind: 'execution.result',
          groupId: 'g1',
          id: 'checkpoint-1',
          ts: new Date().toISOString(),
          correlationId: 'cid-checkpoint',
          method: 'state.checkpoint',
          result: { checkpoint_id: 'ckpt-1' },
        },
        ...makeEntries([{ correlationId: 'cid-1' }]),
      ],
      resetWorkspace: vi.fn(),
      workspaceDispatch,
      restoreSandbox: vi.fn().mockResolvedValue({ restored_to: 'ckpt-1' }),
    })

    // one execution.result → one dispatch
    expect(dispatched).toBe(1)
    expect(workspaceDispatch).toHaveBeenCalledTimes(1)
  })

  it('dispatches both pairs in order for two call/result entries in the same group', async () => {
    const calls: string[] = []
    const workspaceDispatch = vi.fn((primitives) => {
      calls.push(primitives[0].params.props.sourceExecutionId)
    })

    await replayTimelineGroup({
      groupId: 'g1',
      mode: 'simulate',
      sandboxId: 'sb-1',
      entries: [
        {
          kind: 'execution.result',
          groupId: 'g1',
          id: 'checkpoint-2',
          ts: new Date().toISOString(),
          correlationId: 'cid-checkpoint',
          method: 'state.checkpoint',
          result: { checkpoint_id: 'ckpt-2' },
        },
        ...makeEntries([
          { correlationId: 'cid-first',  result: { content: 'a' } },
          { correlationId: 'cid-second', result: { content: 'b' } },
        ]),
      ],
      resetWorkspace: vi.fn(),
      workspaceDispatch,
      restoreSandbox: vi.fn().mockResolvedValue({ restored_to: 'ckpt-2' }),
    })

    // Both dispatched in insertion order
    expect(calls).toEqual(['cid-first', 'cid-second'])
  })

  it('does NOT dispatch for a result entry whose correlationId has no matching call', async () => {
    const now = new Date().toISOString()
    const entries: TimelineEntry[] = [
      { kind: 'execution.result', groupId: 'g1', id: 'checkpoint-3', ts: now, correlationId: 'cid-checkpoint', method: 'state.checkpoint', result: { checkpoint_id: 'ckpt-3' } },
      // call with correlationId A
      { kind: 'execution.call', groupId: 'g1', id: 'c1', ts: now, correlationId: 'cid-A', method: 'fs.read', params: {} },
      // result with mismatched correlationId B (no matching call)
      { kind: 'execution.result', groupId: 'g1', id: 'r1', ts: now, correlationId: 'cid-B', method: 'fs.read', result: {} },
    ]

    const workspaceDispatch = vi.fn()
    const dispatched = await replayTimelineGroup({
      groupId: 'g1',
      mode: 'simulate',
      sandboxId: 'sb-1',
      entries,
      resetWorkspace: vi.fn(),
      workspaceDispatch,
      restoreSandbox: vi.fn().mockResolvedValue({ restored_to: 'ckpt-3' }),
    })

    // No dispatch — the result's correlationId doesn't match any call
    expect(dispatched).toBe(0)
    expect(workspaceDispatch).not.toHaveBeenCalled()
  })

  it('ui-only mode dispatches only ui entries and ignores execution.result', async () => {
    const now = new Date().toISOString()
    const entries: TimelineEntry[] = [
      ...makeEntries([{ correlationId: 'cid-1' }]),
      { kind: 'ui', groupId: 'g1', id: 'ui-1', ts: now, method: 'ui.panel.open', params: { type: 'trace' } },
    ]

    const workspaceDispatch = vi.fn()
    await replayTimelineGroup({
      groupId: 'g1',
      mode: 'ui-only',
      entries,
      resetWorkspace: vi.fn(),
      workspaceDispatch,
    })

    // Only the ui entry is dispatched
    expect(workspaceDispatch).toHaveBeenCalledTimes(1)
    expect(workspaceDispatch.mock.calls[0][0][0].method).toBe('ui.panel.open')
  })

  it('simulate mode replays read-only groups without requiring a checkpoint restore', async () => {
    const workspaceDispatch = vi.fn()
    const restoreSandbox = vi.fn()

    const dispatched = await replayTimelineGroup({
      groupId: 'g1',
      mode: 'simulate',
      entries: makeEntries([{ correlationId: 'cid-1' }]),
      resetWorkspace: vi.fn(),
      workspaceDispatch,
      restoreSandbox,
    })

    expect(dispatched).toBe(1)
    expect(workspaceDispatch).toHaveBeenCalledTimes(1)
    expect(restoreSandbox).not.toHaveBeenCalled()
  })

  it('halts immediately when sandbox restore fails', async () => {
    await expect(replayTimelineGroup({
      groupId: 'g1',
      mode: 'simulate',
      sandboxId: 'sb-1',
      entries: [
        {
          kind: 'execution.result',
          groupId: 'g1',
          id: 'checkpoint-4',
          ts: new Date().toISOString(),
          correlationId: 'cid-checkpoint',
          method: 'state.checkpoint',
          result: { checkpoint_id: 'ckpt-fail' },
        },
        ...makeEntries([{ correlationId: 'cid-1' }]),
      ],
      resetWorkspace: vi.fn(),
      workspaceDispatch: vi.fn(),
      restoreSandbox: vi.fn().mockRejectedValue(new Error('restore failed')),
    })).rejects.toThrow('restore failed')
  })
})
