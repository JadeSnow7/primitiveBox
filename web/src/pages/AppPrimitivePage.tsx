import { useEffect, useState } from 'react'
import { APIError } from '@/api/client'
import { listAppPrimitives } from '@/api/sandboxes'
import { Badge } from '@/components/shared/Badge'
import { JsonView } from '@/components/shared/JsonView'
import { useSandbox } from '@/hooks/useSandbox'
import type { AppPrimitiveManifest } from '@/types/primitive'

export function AppPrimitivePage() {
  const { selectedSandbox } = useSandbox()
  const [items, setItems] = useState<AppPrimitiveManifest[]>([])
  const [selected, setSelected] = useState<AppPrimitiveManifest | null>(null)
  const [status, setStatus] = useState<'idle' | 'loading' | 'unsupported' | 'ready' | 'error'>('idle')
  const [message, setMessage] = useState<string | null>(null)

  useEffect(() => {
    if (!selectedSandbox) {
      setItems([])
      setSelected(null)
      setStatus('idle')
      setMessage(null)
      return
    }

    let active = true
    setStatus('loading')
    setMessage(null)

    void listAppPrimitives(selectedSandbox.id)
      .then((manifests) => {
        if (!active) return
        setItems(manifests)
        setSelected(manifests[0] ?? null)
        setStatus('ready')
      })
      .catch((error) => {
        if (!active) return
        if (error instanceof APIError && [404, 405, 501].includes(error.status)) {
          setStatus('unsupported')
          setMessage('后端尚未实现 `GET /api/v1/sandboxes/{id}/app-primitives`，因此这里保留显式阻塞态。')
          return
        }
        setStatus('error')
        setMessage(error instanceof Error ? error.message : 'Failed to load app primitives')
      })

    return () => {
      active = false
    }
  }, [selectedSandbox])

  if (!selectedSandbox) {
    return <div className="flex h-full items-center justify-center text-sm text-[var(--text-muted)]">请选择一个 sandbox 查看 app primitives。</div>
  }

  if (status === 'unsupported') {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-2xl rounded-2xl border border-[var(--amber)]/25 bg-[var(--amber-bg)] p-6">
          <div className="text-[11px] uppercase tracking-[0.22em] text-[var(--amber)]">App Primitive Endpoint Missing</div>
          <div className="mt-3 text-lg font-medium">目标 inspector 接口未就绪</div>
          <p className="mt-3 text-[13px] leading-6 text-[var(--text-secondary)]">{message}</p>
        </div>
      </div>
    )
  }

  if (status === 'error') {
    return <div className="p-6 text-[13px] text-[var(--red)]">{message}</div>
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-1 overflow-hidden xl:grid-cols-[320px_minmax(0,1fr)]">
      <aside className="border-b border-[var(--border)] p-4 xl:border-b-0 xl:border-r">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <div className="text-[11px] uppercase tracking-[0.22em] text-[var(--text-muted)]">App Primitives</div>
            <div className="mt-1 text-sm font-medium">Registered manifests</div>
          </div>
          <Badge variant="neutral">{items.length}</Badge>
        </div>
        <div className="space-y-2">
          {status === 'loading' ? <div className="text-[12px] text-[var(--text-muted)]">Loading manifests...</div> : null}
          {items.map((item) => (
            <button
              key={item.name}
              className={`w-full rounded-xl border p-3 text-left transition-colors duration-[120ms] ${
                selected?.name === item.name ? 'border-[var(--blue)] bg-[var(--blue-bg)]' : 'border-[var(--border)] bg-[var(--bg-surface)] hover:bg-[var(--bg-subtle)]'
              }`}
              onClick={() => setSelected(item)}
            >
              <div className="font-mono text-[12px] text-[var(--text-mono)]">{item.name}</div>
              <div className="mt-2 text-[12px] text-[var(--text-secondary)]">{item.description}</div>
            </button>
          ))}
        </div>
      </aside>

      <section className="min-h-0 overflow-y-auto p-4">
        {selected ? (
          <div className="space-y-4">
            <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[13px] text-[var(--text-mono)]">{selected.name}</span>
                <Badge variant="warning">app</Badge>
                <Badge variant="neutral">{selected.app_id}</Badge>
              </div>
              <p className="mt-3 text-[13px] leading-6 text-[var(--text-secondary)]">{selected.description}</p>
            </div>
            <JsonView data={selected} />
          </div>
        ) : (
          <div className="text-[12px] text-[var(--text-muted)]">No app primitives registered.</div>
        )}
      </section>
    </div>
  )
}
