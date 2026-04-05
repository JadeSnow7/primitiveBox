import { useEffect, useState } from 'react'
import { getHealth } from '@/api/client'
import { UserShell } from '@/components/user/UserShell'
import { useGoalStore } from '@/store/goalStore'
import { useSandboxStore } from '@/store/sandboxStore'
import { useUIStore } from '@/store/uiStore'
import { useGoalEventStream } from '@/hooks/useGoalEventStream'

export function UserApp() {
  useGoalEventStream()

  const loadGoals      = useGoalStore((s) => s.load)
  const loadSandboxes  = useSandboxStore((s) => s.load)
  const sandboxes      = useSandboxStore((s) => s.sandboxes)
  const selectSandbox  = useSandboxStore((s) => s.select)
  const setGatewayStatus = useUIStore((s) => s.setGatewayStatus)

  const [sandboxesLoaded, setSandboxesLoaded] = useState(false)

  useEffect(() => {
    void loadGoals()
  }, [loadGoals])

  useEffect(() => {
    void loadSandboxes().then(() => setSandboxesLoaded(true))
  }, [loadSandboxes])

  useEffect(() => {
    if (!sandboxesLoaded) return
    const running = sandboxes.find((s) => s.status === 'running')
    if (running !== undefined) {
      selectSandbox(running.id)
    }
  }, [sandboxesLoaded, sandboxes, selectSandbox])

  useEffect(() => {
    let active = true
    setGatewayStatus('checking')
    void getHealth()
      .then(() => { if (active) setGatewayStatus('online') })
      .catch(() => { if (active) setGatewayStatus('offline') })
    return () => { active = false }
  }, [setGatewayStatus])

  if (sandboxesLoaded && !sandboxes.some((s) => s.status === 'running')) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg-base,#0a0a0a)]">
        <div className="text-center">
          <div className="text-[14px] font-medium text-[var(--text-primary)]">服务未就绪</div>
          <div className="mt-1 text-[12px] text-[var(--text-muted)]">请稍后重试</div>
        </div>
      </div>
    )
  }

  return <UserShell />
}
