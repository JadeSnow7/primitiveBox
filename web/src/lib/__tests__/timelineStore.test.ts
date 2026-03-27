/**
 * timelineStore.test.ts
 *
 * Verifies that:
 *   - append() always auto-generates a unique event id
 *   - correlationId and other entry-specific fields pass through verbatim
 *   - ring buffer drops oldest entry when MAX_ENTRIES is exceeded
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { useTimelineStore } from '@/store/timelineStore'

beforeEach(() => {
  useTimelineStore.getState().clear()
})

describe('timelineStore', () => {
  it('auto-generates a unique id for each appended entry', () => {
    const { append, entries } = useTimelineStore.getState()

    append({ kind: 'ui', groupId: 'g1', method: 'ui.panel.open', params: {} })
    append({ kind: 'ui', groupId: 'g1', method: 'ui.panel.open', params: {} })

    const store = useTimelineStore.getState()
    expect(store.entries).toHaveLength(2)
    const [a, b] = store.entries
    expect(a.id).toBeTruthy()
    expect(b.id).toBeTruthy()
    expect(a.id).not.toBe(b.id)
  })

  it('preserves correlationId on execution.call entries', () => {
    const { append } = useTimelineStore.getState()

    append({
      kind: 'execution.call',
      groupId: 'g1',
      correlationId: 'cid-abc',
      method: 'fs.read',
      params: { path: '/foo' },
    })

    const entry = useTimelineStore.getState().entries[0]
    expect(entry.kind).toBe('execution.call')
    if (entry.kind === 'execution.call') {
      expect(entry.correlationId).toBe('cid-abc')
    }
  })

  it('preserves correlationId on execution.result entries', () => {
    const { append } = useTimelineStore.getState()

    append({
      kind: 'execution.result',
      groupId: 'g1',
      correlationId: 'cid-abc',
      method: 'fs.read',
      result: { content: 'hello' },
    })

    const entry = useTimelineStore.getState().entries[0]
    expect(entry.kind).toBe('execution.result')
    if (entry.kind === 'execution.result') {
      expect(entry.correlationId).toBe('cid-abc')
    }
  })

  it('ring buffer drops oldest entry when MAX=100 is reached', () => {
    const { append } = useTimelineStore.getState()

    // Append 101 entries
    for (let i = 0; i < 101; i++) {
      append({
        kind: 'ui',
        groupId: `g${i}`,
        method: 'ui.panel.open',
        params: { index: i },
      })
    }

    const { entries } = useTimelineStore.getState()
    expect(entries).toHaveLength(100)
    // The first entry dropped — first remaining should be the second one appended (index=1)
    const first = entries[0]
    expect(first.kind).toBe('ui')
    if (first.kind === 'ui') {
      expect((first.params as { index: number }).index).toBe(1)
    }
  })

  it('entriesByGroup filters correctly', () => {
    const { append } = useTimelineStore.getState()

    append({ kind: 'ui', groupId: 'ga', method: 'ui.panel.open', params: {} })
    append({ kind: 'ui', groupId: 'gb', method: 'ui.panel.open', params: {} })
    append({ kind: 'ui', groupId: 'ga', method: 'ui.panel.open', params: {} })

    const result = useTimelineStore.getState().entriesByGroup('ga')
    expect(result).toHaveLength(2)
    result.forEach((e) => expect(e.groupId).toBe('ga'))
  })

  it('bounds oversized execution payload retention deterministically', () => {
    const { append } = useTimelineStore.getState()

    append({
      kind: 'execution.result',
      groupId: 'g-big',
      correlationId: 'cid-big',
      method: 'browser.read',
      result: { html: `<style>body{}</style>${'y'.repeat(5000)}` },
    })

    const entry = useTimelineStore.getState().entries[0]
    expect(entry.kind).toBe('execution.result')
    if (entry.kind === 'execution.result') {
      const html = String((entry.result as Record<string, unknown>).html)
      expect(html).not.toContain('<style')
      expect(html.length).toBeLessThan(4200)
    }
  })
})
