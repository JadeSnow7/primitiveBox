import { Badge } from '@/components/shared/Badge'
import { cn } from '@/lib/utils'
import type { PrimitiveSchema } from '@/types/primitive'

export function PrimitiveList({
  primitives,
  selected,
  onSelect
}: {
  primitives: PrimitiveSchema[]
  selected: string | null
  onSelect: (primitive: PrimitiveSchema) => void
}) {
  return (
    <div className="space-y-2">
      {primitives.map((primitive) => (
        <button
          key={primitive.name}
          className={cn(
            'w-full rounded-xl border p-3 text-left transition-colors duration-[120ms]',
            selected === primitive.name
              ? 'border-[var(--blue)] bg-[var(--blue-bg)]'
              : 'border-[var(--border)] bg-[var(--bg-surface)] hover:bg-[var(--bg-subtle)]'
          )}
          onClick={() => onSelect(primitive)}
        >
          <div className="flex items-center justify-between gap-3">
            <span className="font-mono text-[12px] text-[var(--text-mono)]">{primitive.name}</span>
            <Badge variant={primitive.kind === 'app' ? 'warning' : 'neutral'}>{primitive.kind}</Badge>
          </div>
          <div className="mt-2 text-[12px] leading-5 text-[var(--text-secondary)]">{primitive.description}</div>
        </button>
      ))}
    </div>
  )
}
