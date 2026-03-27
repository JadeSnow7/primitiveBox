import { beforeEach, describe, expect, it } from 'vitest'
import { DataTableWidget } from '@/components/workspace/widgets/DataTableWidget'
import { MarkdownWidget } from '@/components/workspace/widgets/MarkdownWidget'
import { RawJsonWidget } from '@/components/workspace/widgets/RawJsonWidget'
import { useUIRegistryStore } from '@/lib/uiRegistryStore'

describe('uiRegistryStore', () => {
  beforeEach(() => {
    useUIRegistryStore.getState().reset()
  })

  it('resolves table hint to the data table widget', () => {
    const view = useUIRegistryStore.getState().resolveView({
      primitiveName: 'demo.tabular_data',
      uiLayoutHint: 'table',
    })
    expect(view).toBe(DataTableWidget)
  })

  it('falls back to raw json widget when no renderer matches', () => {
    const view = useUIRegistryStore.getState().resolveView({
      primitiveName: 'unknown.primitive',
    })
    expect(view).toBe(RawJsonWidget)
  })

  it('resolves markdown hint to the markdown widget', () => {
    const view = useUIRegistryStore.getState().resolveView({
      primitiveName: 'browser.read',
      uiLayoutHint: 'markdown',
    })
    expect(view).toBe(MarkdownWidget)
  })
})
