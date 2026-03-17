import type { ReactNode } from 'react'
import { CreateDialog } from '@/components/sandbox/CreateDialog'
import { SandboxList } from '@/components/sandbox/SandboxList'
import { Sidebar } from '@/components/layout/Sidebar'
import { Topbar } from '@/components/layout/Topbar'
import { useUIStore } from '@/store/uiStore'

type View = 'trace' | 'primitives' | 'app-primitives'

export function Shell({
  view,
  onViewChange,
  children
}: {
  view: View
  onViewChange: (view: View) => void
  children: ReactNode
}) {
  const createDialogOpen = useUIStore((s) => s.createDialogOpen)
  const setCreateDialogOpen = useUIStore((s) => s.setCreateDialogOpen)

  return (
    <div className="min-h-screen p-4 md:p-5">
      <div className="mx-auto grid min-h-[calc(100vh-2rem)] max-w-[1600px] grid-cols-1 gap-4 lg:grid-cols-[228px_280px_minmax(0,1fr)]">
        <Sidebar view={view} onViewChange={onViewChange} />
        <SandboxList />
        <div className="flex min-h-[70vh] flex-col gap-4">
          <Topbar />
          <section className="panel-surface min-h-0 flex-1 overflow-hidden">{children}</section>
        </div>
      </div>

      {createDialogOpen ? (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-[rgba(10,10,10,0.45)] p-4">
          <div className="w-full max-w-md">
            <CreateDialog onClose={() => setCreateDialogOpen(false)} />
          </div>
        </div>
      ) : null}
    </div>
  )
}
