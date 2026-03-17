import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { useSandboxStore } from '@/store/sandboxStore'

interface CreateParams {
  driver: 'docker'
  workspace: string
  ttl: number
}

export function CreateDialog({ onClose }: { onClose: () => void }) {
  const [params, setParams] = useState<CreateParams>({
    driver: 'docker',
    workspace: './my-project',
    ttl: 3600
  })
  const [loading, setLoading] = useState(false)
  const createSandbox = useSandboxStore((s) => s.create)

  async function handleSubmit() {
    setLoading(true)
    try {
      await createSandbox(params)
      onClose()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="panel-surface rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-surface)] p-5">
      <div className="mb-4 text-[13px] font-medium">新建 Sandbox</div>

      <div className="space-y-3">
        <Field label="workspace">
          <input className="field-input" value={params.workspace} onChange={(e) => setParams((p) => ({ ...p, workspace: e.target.value }))} />
        </Field>

        <Field label="driver">
          <select
            className="field-input"
            value={params.driver}
            onChange={(e) => setParams((p) => ({ ...p, driver: e.target.value as 'docker' }))}
          >
            <option value="docker">docker</option>
          </select>
        </Field>

        <Field label="ttl (s)">
          <input
            type="number"
            className="field-input"
            value={params.ttl}
            onChange={(e) => setParams((p) => ({ ...p, ttl: Number(e.target.value) }))}
          />
        </Field>
      </div>

      <div className="mt-5 flex gap-2">
        <Button variant="ghost" className="flex-1" onClick={onClose}>
          取消
        </Button>
        <Button className="flex-1" onClick={() => void handleSubmit()} disabled={loading}>
          {loading ? '创建中...' : '创建'}
        </Button>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <label className="text-[11px] font-medium tracking-[0.18em] text-[var(--text-muted)]">{label.toUpperCase()}</label>
      {children}
    </div>
  )
}
