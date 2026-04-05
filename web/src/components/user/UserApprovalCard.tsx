import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { useGoalStore } from '@/store/goalStore'
import type { Goal, GoalReview } from '@/types/goal'

const BORDER_COLOR: Record<string, string> = {
  low:    'border-[var(--border)]',
  medium: 'border-orange-400',
  high:   'border-red-500',
}

export function UserApprovalCard({ goal, review }: { goal: Goal; review: GoalReview }) {
  const approve = useGoalStore((s) => s.approve)
  const resume  = useGoalStore((s) => s.resume)
  const reject  = useGoalStore((s) => s.reject)

  const [loading, setLoading] = useState<'approve' | 'reject' | null>(null)
  const [error, setError]     = useState<string | null>(null)

  const borderColor = BORDER_COLOR[review.risk_level] ?? 'border-orange-400'

  async function handleApprove() {
    setLoading('approve')
    setError(null)
    try {
      await approve(goal.id, review.id)
      await resume(goal.id)
    } catch {
      setError('操作失败，请重试')
    } finally {
      setLoading(null)
    }
  }

  async function handleReject() {
    setLoading('reject')
    setError(null)
    try {
      await reject(goal.id, review.id, undefined)
    } catch {
      setError('操作失败，请重试')
    } finally {
      setLoading(null)
    }
  }

  return (
    <div className={`rounded-lg border ${borderColor} bg-[var(--bg-raised)] p-4`}>
      <div className="mb-2 text-[12px] font-semibold text-orange-400">⏸ AI 需要你确认后继续</div>
      <div className="mb-1 text-[12px] text-[var(--text-primary)]">即将执行：</div>
      <div className="mb-3 border-l-2 border-[var(--border)] pl-3 text-[12px] text-[var(--text-secondary)]">
        {review.side_effect ?? review.primitive}
      </div>
      {!review.reversible && (
        <div className="mb-3 text-[11px] text-red-400">此操作不可撤销</div>
      )}
      {error !== null && (
        <div className="mb-2 text-[11px] text-red-400">{error}</div>
      )}
      <div className="flex gap-2">
        <Button
          size="sm"
          variant="subtle"
          className="flex-1 border-[var(--green,#4ade80)] text-[var(--green,#4ade80)]"
          disabled={loading !== null}
          onClick={() => void handleApprove()}
        >
          {loading === 'approve' ? '处理中…' : '批准'}
        </Button>
        <Button
          size="sm"
          variant="subtle"
          className="flex-1"
          disabled={loading !== null}
          onClick={() => void handleReject()}
        >
          {loading === 'reject' ? '处理中…' : '拒绝'}
        </Button>
      </div>
    </div>
  )
}
