import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePrimitiveStore } from '@/store/primitiveStore'

vi.mock('@/api/primitives', () => ({
  listPrimitives: vi.fn(),
}))

import { listPrimitives } from '@/api/primitives'

describe('primitiveStore', () => {
  beforeEach(() => {
    usePrimitiveStore.getState().reset()
    vi.clearAllMocks()
  })

  it('hydrates the primitive catalog from the gateway response', async () => {
    vi.mocked(listPrimitives).mockResolvedValue([
      {
        name: 'email.send',
        description: 'Send email',
        kind: 'app',
        input_schema: {},
        output_schema: {},
        ui_layout_hint: 'table',
        intent: {
          category: 'mutation',
          side_effect: 'external',
          reversible: false,
          risk_level: 'high',
        },
      },
    ])

    await usePrimitiveStore.getState().load()

    expect(usePrimitiveStore.getState().status).toBe('ready')
    expect(usePrimitiveStore.getState().getPrimitive('email.send')?.intent.risk_level).toBe('high')
    expect(usePrimitiveStore.getState().getPrimitive('email.send')?.ui_layout_hint).toBe('table')
  })

  it('fails closed when hydration fails', async () => {
    vi.mocked(listPrimitives).mockRejectedValue(new Error('gateway offline'))

    await usePrimitiveStore.getState().load()

    expect(usePrimitiveStore.getState().status).toBe('error')
    expect(usePrimitiveStore.getState().error).toContain('gateway offline')
    expect(usePrimitiveStore.getState().getPrimitive('email.send')).toBeNull()
  })
})
