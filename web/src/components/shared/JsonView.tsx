import { JsonView as JSONViewLite } from 'react-json-view-lite'

export function JsonView({ data }: { data: unknown }) {
  const safeData = Array.isArray(data) || (data !== null && typeof data === 'object') ? data : { value: data }

  return (
    <div className="rounded-xl border border-[var(--border)] bg-[color-mix(in_srgb,var(--bg-surface)_92%,transparent)] p-3">
      <JSONViewLite data={safeData} shouldExpandNode={(level) => level < 2} />
    </div>
  )
}
