import { useEffect, useState } from 'react'
import type { RJSFSchema } from '@rjsf/utils'
import { callPrimitive, listPrimitives } from '@/api/primitives'
import { PrimitiveList } from '@/components/primitives/PrimitiveList'
import { ResponseView } from '@/components/primitives/ResponseView'
import { SchemaForm } from '@/components/primitives/SchemaForm'
import { Badge } from '@/components/shared/Badge'
import { useSandbox } from '@/hooks/useSandbox'
import type { PrimitiveSchema } from '@/types/primitive'

export function PrimitivePanel() {
  const { selectedSandbox } = useSandbox()
  const [primitives, setPrimitives] = useState<PrimitiveSchema[]>([])
  const [selectedPrimitive, setSelectedPrimitive] = useState<PrimitiveSchema | null>(null)
  const [result, setResult] = useState<unknown | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [fetching, setFetching] = useState(true)

  useEffect(() => {
    let active = true
    setFetching(true)
    void listPrimitives()
      .then((items) => {
        if (!active) return
        setPrimitives(items)
        setSelectedPrimitive(items[0] ?? null)
      })
      .catch((requestError) => {
        if (!active) return
        setError(requestError instanceof Error ? requestError.message : 'Failed to load primitives')
      })
      .finally(() => {
        if (active) setFetching(false)
      })
    return () => {
      active = false
    }
  }, [])

  async function handleSubmit(formData: object) {
    if (!selectedSandbox || !selectedPrimitive) return
    setLoading(true)
    setError(null)
    try {
      const nextResult = await callPrimitive<unknown>(selectedSandbox.id, selectedPrimitive.name, formData)
      setResult(nextResult)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Primitive execution failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-1 overflow-hidden xl:grid-cols-[320px_minmax(0,1fr)]">
      <aside className="border-b border-[var(--border)] p-4 xl:border-b-0 xl:border-r">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <div className="text-[11px] uppercase tracking-[0.22em] text-[var(--text-muted)]">Primitives</div>
            <div className="mt-1 text-sm font-medium">System registry</div>
          </div>
          <Badge variant="neutral">{primitives.length}</Badge>
        </div>

        {fetching ? <div className="text-[12px] text-[var(--text-muted)]">Loading primitives...</div> : <PrimitiveList primitives={primitives} selected={selectedPrimitive?.name ?? null} onSelect={setSelectedPrimitive} />}
      </aside>

      <section className="min-h-0 overflow-y-auto p-4">
        {!selectedSandbox ? (
          <div className="rounded-xl border border-[var(--amber)]/25 bg-[var(--amber-bg)] p-4 text-[12px] text-[var(--amber)]">
            请选择一个运行中的 sandbox，然后再执行原语。
          </div>
        ) : null}

        {selectedPrimitive ? (
          <div className="space-y-4">
            <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[13px] text-[var(--text-mono)]">{selectedPrimitive.name}</span>
                <Badge variant="neutral">{selectedPrimitive.namespace ?? 'primitive'}</Badge>
                {selectedPrimitive.scope ? <Badge variant="checkpoint">{selectedPrimitive.scope}</Badge> : null}
              </div>
              <p className="mt-3 max-w-3xl text-[13px] leading-6 text-[var(--text-secondary)]">{selectedPrimitive.description}</p>
            </div>

            <SchemaForm schema={(selectedPrimitive.input_schema || {}) as RJSFSchema} onSubmit={(data) => void handleSubmit(data)} loading={loading || !selectedSandbox} />

            <ResponseView result={result} error={error} />
          </div>
        ) : (
          <div className="text-[12px] text-[var(--text-muted)]">No primitives found.</div>
        )}
      </section>
    </div>
  )
}
