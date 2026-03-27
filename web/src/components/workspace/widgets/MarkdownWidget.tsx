import type { PrimitiveSchema } from '@/types/primitive'
import type { WorkspacePanel } from '@/types/workspace'

interface MarkdownWidgetProps {
  panel: WorkspacePanel
  result: unknown
  primitive: PrimitiveSchema | null
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function resolveContent(result: unknown): string {
  if (typeof result === 'string') return result
  const root = asRecord(result)
  if (!root) return JSON.stringify(result, null, 2)
  for (const key of ['markdown', 'text', 'content']) {
    const candidate = root[key]
    if (typeof candidate === 'string' && candidate.trim()) return candidate
  }
  return JSON.stringify(result, null, 2)
}

export function MarkdownWidget({ panel, result, primitive }: MarkdownWidgetProps) {
  const title =
    (typeof panel.props['title'] === 'string' && panel.props['title']) ||
    primitive?.name ||
    (typeof panel.props['method'] === 'string' && panel.props['method']) ||
    'Markdown Result'
  const content = resolveContent(result)

  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">{title}</div>
      <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] p-3 text-[12px] leading-6 text-[var(--text-secondary)]">
        {content}
      </pre>
    </div>
  )
}
