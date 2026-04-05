import { useState } from 'react'
import { UserGoalList } from '@/components/user/UserGoalList'
import { UserGoalInput } from '@/components/user/UserGoalInput'
import { UserExecutionView } from '@/components/user/UserExecutionView'
import { useUIStore } from '@/store/uiStore'

function connectionDot(status: 'checking' | 'online' | 'offline'): string {
  if (status === 'online')  return 'bg-[var(--green,#4ade80)]'
  if (status === 'offline') return 'bg-red-500'
  return 'bg-[var(--text-muted)] animate-pulse'
}

function connectionLabel(status: 'checking' | 'online' | 'offline'): string {
  if (status === 'online')  return '已连接'
  if (status === 'offline') return '连接断开'
  return '连接中…'
}

export function UserShell() {
  const gatewayStatus = useUIStore((s) => s.gatewayStatus)
  const [showInput, setShowInput] = useState(false)

  return (
    <div className="flex min-h-screen flex-col bg-[var(--bg-base,#0a0a0a)]">
      {/* Topbar */}
      <header className="flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg-surface,#111)] px-5 py-3">
        <div className="flex items-center gap-3">
          <span className="text-[14px] font-semibold text-[var(--text-primary)]">PrimitiveBox</span>
          <span className="text-[11px] text-[var(--text-muted)]">AI 任务助手</span>
        </div>
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${connectionDot(gatewayStatus)}`} />
          <span className="text-[11px] text-[var(--text-muted)]">{connectionLabel(gatewayStatus)}</span>
        </div>
      </header>

      {/* Body */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left panel */}
        <div className="flex w-[260px] flex-shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-subtle,#0d0d0d)]">
          {showInput && <UserGoalInput onClose={() => setShowInput(false)} />}
          <div className="min-h-0 flex-1 overflow-hidden">
            <UserGoalList onNewGoal={() => setShowInput(true)} />
          </div>
        </div>

        {/* Right panel */}
        <div className="flex flex-1 flex-col overflow-hidden bg-[var(--bg-surface,#111)]">
          <UserExecutionView />
        </div>
      </div>
    </div>
  )
}
