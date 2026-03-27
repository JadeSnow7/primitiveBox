import { beforeEach, describe, expect, it } from 'vitest'
import { mapExecutionResultToUI } from '@/lib/executionMapper'
import { usePrimitiveStore } from '@/store/primitiveStore'
import type { PrimitiveSchema } from '@/types/primitive'

function seedPrimitives(primitives: PrimitiveSchema[]) {
  usePrimitiveStore.setState({
    status: 'ready',
    error: null,
    primitives,
    primitivesByName: Object.fromEntries(primitives.map((primitive) => [primitive.name, primitive])),
  })
}

describe('executionMapper', () => {
  beforeEach(() => {
    usePrimitiveStore.getState().reset()
  })

  it('maps demo.tabular_data to a primitive panel carrying table ui hint', () => {
    seedPrimitives([
      {
        name: 'demo.tabular_data',
        description: 'Demo rows',
        kind: 'app',
        input_schema: {},
        output_schema: {},
        ui_layout_hint: 'table',
        intent: { category: 'query', side_effect: 'read', reversible: true, risk_level: 'low' },
      },
    ])

    const mapped = mapExecutionResultToUI(
      'demo.tabular_data',
      {},
      { rows: [{ id: 1, name: 'alpha' }] },
      'exec-1',
    )

    expect(mapped).toHaveLength(1)
    const open = mapped[0]
    expect(open.method).toBe('ui.panel.open')
    if (open.method !== 'ui.panel.open') throw new Error('expected ui.panel.open')
    expect(open.params.type).toBe('primitive')
    expect(open.params.props?.['method']).toBe('demo.tabular_data')
    expect(open.params.props?.['uiLayoutHint']).toBe('table')
  })

  it('falls back to generic primitive panel for unknown primitives', () => {
    seedPrimitives([])

    const mapped = mapExecutionResultToUI('unknown.primitive', {}, { ok: true }, 'exec-2')

    expect(mapped).toHaveLength(1)
    const open = mapped[0]
    expect(open.method).toBe('ui.panel.open')
    if (open.method !== 'ui.panel.open') throw new Error('expected ui.panel.open')
    expect(open.params.type).toBe('primitive')
    expect(open.params.props?.['method']).toBe('unknown.primitive')
    expect(open.params.props?.['uiLayoutHint']).toBeUndefined()
  })

  it('bounds oversized retained results before opening a panel', () => {
    seedPrimitives([])

    const mapped = mapExecutionResultToUI(
      'unknown.primitive',
      {},
      { html: `<script>boom()</script>${'x'.repeat(5000)}` },
      'exec-3',
    )

    expect(mapped).toHaveLength(1)
    const open = mapped[0]
    if (open.method !== 'ui.panel.open') throw new Error('expected ui.panel.open')
    const result = open.params.props?.['result'] as Record<string, unknown>
    expect(String(result.html)).not.toContain('<script')
    expect(String(result.html).length).toBeLessThan(4200)
  })
})
