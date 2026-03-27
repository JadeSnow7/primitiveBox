import { JsonView } from '@/components/shared/JsonView'
import type { PrimitiveSchema } from '@/types/primitive'
import type { WorkspacePanel } from '@/types/workspace'

interface RawJsonWidgetProps {
  panel: WorkspacePanel
  result: unknown
  primitive: PrimitiveSchema | null
}

export function RawJsonWidget({ panel, result, primitive }: RawJsonWidgetProps) {
  const title =
    (typeof panel.props['title'] === 'string' && panel.props['title']) ||
    primitive?.name ||
    (typeof panel.props['method'] === 'string' && panel.props['method']) ||
    'Result'

  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
        {title}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <JsonView data={result} />
      </div>
    </div>
  )
}
