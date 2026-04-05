import { useGoalStore } from '@/store/goalStore'
import { formatStepLabel } from '@/lib/stepFormatter'
import { UserApprovalCard } from '@/components/user/UserApprovalCard'
import type { GoalStepStatus } from '@/types/goal'

const STEP_ICON: Record<GoalStepStatus, string> = {
  pending:        '○',
  running:        '◌',
  passed:         '✓',
  failed:         '✗',
  awaiting_review:'⏸',
  skipped:        '—',
  rolled_back:    '↩',
}

const STEP_COLOR: Record<GoalStepStatus, string> = {
  pending:        'text-[var(--text-muted)]',
  running:        'text-blue-400',
  passed:         'text-[var(--green,#4ade80)]',
  failed:         'text-red-400',
  awaiting_review:'text-orange-400',
  skipped:        'text-[var(--text-muted)]',
  rolled_back:    'text-[var(--text-muted)]',
}

export function UserExecutionView() {
  const goals      = useGoalStore((s) => s.goals)
  const selectedId = useGoalStore((s) => s.selectedId)
  const goal       = goals.find((g) => g.id === selectedId) ?? null

  if (goal === null) {
    return (
      <div className="flex h-full items-center justify-center text-[13px] text-[var(--text-muted)]">
        选择一个任务或新建任务开始执行
      </div>
    )
  }

  const pendingReview  = goal.reviews?.find((r) => r.status === 'pending') ?? null
  const sortedSteps    = [...(goal.steps ?? [])].sort((a, b) => a.seq - b.seq)

  return (
    <div className="flex h-full flex-col gap-3 overflow-y-auto p-4">
      <div className="text-[13px] font-medium text-[var(--text-secondary)]">
        {goal.description}
      </div>

      {sortedSteps.length === 0 && (goal.status === 'executing' || goal.status === 'created') && (
        <div className="text-[12px] text-[var(--text-muted)]">正在规划…</div>
      )}

      <div className="flex flex-col gap-2">
        {sortedSteps.map((step) => (
          <div
            key={step.id}
            className="flex items-center gap-3 rounded-md border border-[var(--border)] bg-[var(--bg-raised)] px-3 py-2"
          >
            <span className={`w-4 flex-shrink-0 text-center font-mono text-[13px] ${STEP_COLOR[step.status]}`}>
              {STEP_ICON[step.status]}
            </span>
            <span className="text-[12px] text-[var(--text-secondary)]">
              {formatStepLabel(step)}
            </span>
          </div>
        ))}
      </div>

      {pendingReview !== null && (
        <UserApprovalCard goal={goal} review={pendingReview} />
      )}

      {goal.status === 'completed' && (
        <div className="rounded-md border border-[var(--green,#4ade80)] bg-[rgba(74,222,128,0.08)] px-3 py-2 text-[12px] text-[var(--green,#4ade80)]">
          任务已完成
        </div>
      )}

      {goal.status === 'failed' && (
        <div className="rounded-md border border-red-500 bg-[rgba(239,68,68,0.08)] px-3 py-2 text-[12px] text-red-400">
          执行过程中出现错误
        </div>
      )}
    </div>
  )
}
