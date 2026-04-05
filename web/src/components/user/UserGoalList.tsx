import { useGoalStore } from '@/store/goalStore'
import { Button } from '@/components/ui/button'
import type { GoalStatus } from '@/types/goal'

const STATUS_BADGE: Record<GoalStatus, { label: string; className: string }> = {
  created:   { label: '未开始', className: 'text-[var(--text-muted)]' },
  executing: { label: '执行中', className: 'text-blue-400' },
  verifying: { label: '执行中', className: 'text-blue-400' },
  paused:    { label: '待确认', className: 'text-orange-400' },
  completed: { label: '已完成', className: 'text-[var(--green,#4ade80)]' },
  failed:    { label: '失败',   className: 'text-red-400' },
}

export function UserGoalList({ onNewGoal }: { onNewGoal: () => void }) {
  const goals      = useGoalStore((s) => s.goals)
  const selectedId = useGoalStore((s) => s.selectedId)
  const select     = useGoalStore((s) => s.select)

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-[var(--border)] p-3">
        <Button size="sm" className="w-full" onClick={onNewGoal}>
          + 新建任务
        </Button>
      </div>
      <div className="flex-1 space-y-1 overflow-y-auto p-2">
        {goals.map((goal) => {
          const badge = STATUS_BADGE[goal.status] ?? { label: goal.status, className: 'text-[var(--text-muted)]' }
          const isSelected = goal.id === selectedId
          return (
            <button
              key={goal.id}
              data-goal-id={goal.id}
              onClick={() => select(goal.id)}
              className={`w-full rounded-lg border px-3 py-2.5 text-left transition-colors ${
                isSelected
                  ? 'border-[var(--blue)] bg-[var(--blue-bg)]'
                  : 'border-[var(--border)] bg-[var(--bg-raised)] hover:bg-[var(--bg-subtle)]'
              }`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-[12px] font-medium text-[var(--text-primary)]">
                  {goal.description}
                </span>
                <span className={`flex-shrink-0 text-[10px] ${badge.className}`}>
                  {badge.label}
                </span>
              </div>
            </button>
          )
        })}
        {goals.length === 0 && (
          <div className="py-8 text-center text-[12px] text-[var(--text-muted)]">
            还没有任务
          </div>
        )}
      </div>
    </div>
  )
}
