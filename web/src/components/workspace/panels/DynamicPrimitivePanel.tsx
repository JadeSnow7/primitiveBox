import { useMemo } from 'react'
import { resolvePrimitiveResult } from '@/lib/executionMapper'
import { useUIRegistryStore } from '@/lib/uiRegistryStore'
import { usePrimitiveStore } from '@/store/primitiveStore'
import type { WorkspacePanel } from '@/types/workspace'

export function DynamicPrimitivePanel({ panel }: { panel: WorkspacePanel }) {
  const primitiveName = typeof panel.props['method'] === 'string' ? panel.props['method'] : undefined
  const primitive = usePrimitiveStore((state) => (primitiveName ? state.getPrimitive(primitiveName) : null))
  const result = resolvePrimitiveResult(panel.props)
  const uiLayoutHint =
    (typeof panel.props['uiLayoutHint'] === 'string' && panel.props['uiLayoutHint']) ||
    primitive?.ui_layout_hint

  const Widget = useMemo(
    () =>
      useUIRegistryStore.getState().resolveView({
        primitiveName,
        uiLayoutHint,
        outputSchema: primitive?.output_schema,
      }),
    [primitive?.output_schema, primitiveName, uiLayoutHint],
  )

  return <Widget panel={panel} result={result} primitive={primitive} />
}
