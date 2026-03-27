import { JsonView } from '@/components/shared/JsonView'
import type { PrimitiveSchema } from '@/types/primitive'
import type { WorkspacePanel } from '@/types/workspace'

interface DataTableWidgetProps {
  panel: WorkspacePanel
  result: unknown
  primitive: PrimitiveSchema | null
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function extractRows(result: unknown): Record<string, unknown>[] {
  if (Array.isArray(result)) {
    return result.filter((row): row is Record<string, unknown> => row !== null && typeof row === 'object' && !Array.isArray(row))
  }
  const root = asRecord(result)
  if (!root) return []
  for (const key of ['rows', 'items', 'data']) {
    const candidate = root[key]
    if (!Array.isArray(candidate)) continue
    const rows = candidate.filter(
      (row): row is Record<string, unknown> => row !== null && typeof row === 'object' && !Array.isArray(row),
    )
    if (rows.length > 0) return rows
  }
  return []
}

function collectColumns(rows: Record<string, unknown>[]): string[] {
  const cols = new Set<string>()
  for (const row of rows) {
    for (const key of Object.keys(row)) cols.add(key)
  }
  return Array.from(cols)
}

export function DataTableWidget({ panel, result, primitive }: DataTableWidgetProps) {
  const rows = extractRows(result)
  const columns = collectColumns(rows)
  const title =
    (typeof panel.props['title'] === 'string' && panel.props['title']) ||
    primitive?.name ||
    (typeof panel.props['method'] === 'string' && panel.props['method']) ||
    'Table Result'

  return (
    <div className="flex h-full flex-col gap-3 p-3">
      <div className="flex items-center justify-between">
        <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">{title}</div>
        <div className="font-mono text-[10px] text-[var(--text-muted)]">{rows.length} rows</div>
      </div>
      {rows.length > 0 && columns.length > 0 ? (
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]">
          <table className="w-full border-collapse text-left text-[12px]">
            <thead className="sticky top-0 bg-[var(--bg-surface)]">
              <tr>
                {columns.map((column) => (
                  <th key={column} className="border-b border-[var(--border)] px-3 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
                    {column}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, rowIndex) => (
                <tr key={rowIndex} className="border-b border-[var(--border)] last:border-b-0">
                  {columns.map((column) => (
                    <td key={`${rowIndex}-${column}`} className="px-3 py-2 align-top text-[var(--text-secondary)]">
                      {typeof row[column] === 'string' || typeof row[column] === 'number' || typeof row[column] === 'boolean'
                        ? String(row[column])
                        : JSON.stringify(row[column])}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <JsonView data={result} />
        </div>
      )}
    </div>
  )
}
