import { JsonView } from '@/components/shared/JsonView'

export function ResponseView({ result, error }: { result: unknown; error: string | null }) {
  if (error) {
    return <div className="rounded-xl border border-[var(--red)]/20 bg-[var(--red-bg)] p-4 text-[12px] text-[var(--red)]">{error}</div>
  }

  if (result === null) {
    return (
      <div className="rounded-xl border border-dashed border-[var(--border-strong)] p-5 text-center text-[12px] text-[var(--text-muted)]">
        执行结果会显示在这里。
      </div>
    )
  }

  return <JsonView data={result} />
}
