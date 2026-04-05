import { useState } from 'react'
import { useGoalStore } from '@/store/goalStore'
import { useSandboxStore } from '@/store/sandboxStore'
import { Button } from '@/components/ui/button'

export function UserGoalInput({ onClose }: { onClose: () => void }) {
  const [description, setDescription] = useState('')
  const [submitting, setSubmitting]   = useState(false)
  const [error, setError]             = useState<string | null>(null)

  const create            = useGoalStore((s) => s.create)
  const execute           = useGoalStore((s) => s.execute)
  const select            = useGoalStore((s) => s.select)
  const selectedSandboxId = useSandboxStore((s) => s.selectedId)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!description.trim() || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const goal = await create({
        description: description.trim(),
        packages: [],
        sandbox_ids: selectedSandboxId !== null ? [selectedSandboxId] : [],
      })
      select(goal.id)
      await execute(goal.id)
      onClose()
    } catch {
      setError('任务创建失败，请重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="border-b border-[var(--border)] bg-[var(--bg-raised)] p-3">
      <div className="mb-2 text-[11px] font-medium text-[var(--text-secondary)]">新建任务</div>
      <form onSubmit={(e) => void handleSubmit(e)}>
        <textarea
          className="mb-2 w-full resize-none rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--blue)] focus:outline-none"
          rows={3}
          placeholder="描述你想完成的任务…"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          disabled={submitting}
          autoFocus
        />
        {error !== null && (
          <div className="mb-2 text-[11px] text-red-400">{error}</div>
        )}
        <div className="flex gap-2">
          <Button
            size="sm"
            type="submit"
            disabled={submitting || !description.trim()}
            className="flex-1"
          >
            {submitting ? '创建中…' : '开始执行'}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            type="button"
            disabled={submitting}
            onClick={onClose}
          >
            取消
          </Button>
        </div>
      </form>
    </div>
  )
}
