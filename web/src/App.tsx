import { useEffect, useState } from 'react'
import { getHealth } from '@/api/client'
import { Shell } from '@/components/layout/Shell'
import { AppPrimitivePage } from '@/pages/AppPrimitivePage'
import { PrimitivePage } from '@/pages/PrimitivePage'
import { TracePage } from '@/pages/TracePage'
import { WorkspacePage } from '@/pages/WorkspacePage'
import { useSandboxStore } from '@/store/sandboxStore'
import { useUIStore } from '@/store/uiStore'

type View = 'trace' | 'primitives' | 'app-primitives' | 'workspace'

export default function App() {
  const [view, setView] = useState<View>('trace')
  const loadSandboxes = useSandboxStore((s) => s.load)
  const refreshSelected = useSandboxStore((s) => s.refreshSelected)
  const setGatewayStatus = useUIStore((s) => s.setGatewayStatus)

  useEffect(() => {
    void loadSandboxes()
  }, [loadSandboxes])

  useEffect(() => {
    void refreshSelected()
  }, [refreshSelected])

  useEffect(() => {
    let active = true
    setGatewayStatus('checking')
    void getHealth()
      .then(() => {
        if (active) setGatewayStatus('online')
      })
      .catch(() => {
        if (active) setGatewayStatus('offline')
      })
    return () => {
      active = false
    }
  }, [setGatewayStatus])

  return (
    <Shell view={view} onViewChange={setView}>
      {view === 'trace' ? <TracePage /> : null}
      {view === 'primitives' ? <PrimitivePage /> : null}
      {view === 'app-primitives' ? <AppPrimitivePage /> : null}
      {view === 'workspace' ? <WorkspacePage /> : null}
    </Shell>
  )
}
