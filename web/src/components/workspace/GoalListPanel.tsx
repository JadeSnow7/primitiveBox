import { useEffect, useState } from 'react'
import { useGoalStore } from '@/store/goalStore'
import { useWorkspaceStore } from '@/store/workspaceStore'
import type { GoalStatus } from '@/types/goal'

function GoalStatusDot({ status }: { status: GoalStatus }) {
  const colors: Record<GoalStatus, string> = {
    created:   'bg-[var(--text-muted)]',
    executing: 'bg-amber-400 animate-pulse',
    verifying: 'bg-blue-400',
    completed: 'bg-[var(--teal)]',
    failed:    'bg-red-500',
    paused:    'bg-orange-400',
  }
  return (
    <span
      className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${colors[status] ?? 'bg-gray-400'}`}
    />
  )
}

export function GoalListPanel() {
  const { goals, loading, create, select, load } = useGoalStore()
  const dispatch = useWorkspaceStore((s) => s.dispatch)
  const [showForm, setShowForm] = useState(false)
  const [desc, setDesc] = useState('')
  const [pkgs, setPkgs] = useState('')
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    void load()
  }, [load])

  function openGoal(goalId: string) {
    select(goalId)
    dispatch([{ method: 'ui.panel.open', params: { type: 'goal', props: { goalId } } }])
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!desc.trim()) return
    setCreating(true)
    try {
      const goal = await create({
        description: desc.trim(),
        packages: pkgs.split(',').map((p) => p.trim()).filter(Boolean),
        sandbox_ids: [],
      })
      setDesc('')
      setPkgs('')
      setShowForm(false)
      openGoal(goal.id)
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="flex flex-col gap-1">
      {/* Header row */}
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
          Goals
        </span>
        <div className="flex items-center gap-1">
          {loading && (
            <span className="font-mono text-[9px] text-[var(--text-muted)]">…</span>
          )}
          <button
            onClick={() => setShowForm((v) => !v)}
            className="rounded px-1.5 py-0.5 text-[10px] text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text-primary)]"
          >
            {showForm ? '✕' : '+ New'}
          </button>
        </div>
      </div>

      {/* Inline create form */}
      {showForm && (
        <form
          onSubmit={(e) => void handleCreate(e)}
          className="flex flex-col gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] p-2"
        >
          <input
            autoFocus
            value={desc}
            onChange={(e) => setDesc(e.target.value)}
            placeholder="Description"
            className="rounded border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none"
          />
          <input
            value={pkgs}
            onChange={(e) => setPkgs(e.target.value)}
            placeholder="Packages (comma-separated)"
            className="rounded border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none"
          />
          <button
            type="submit"
            disabled={creating || !desc.trim()}
            className="rounded bg-[var(--blue)]/10 px-2 py-0.5 text-[11px] text-[var(--blue)] hover:bg-[var(--blue)]/20 disabled:opacity-40"
          >
            {creating ? 'Creating…' : 'Create'}
          </button>
        </form>
      )}

      {/* Goal list */}
      <div className="flex flex-col gap-0.5">
        {goals.length === 0 && !loading && (
          <span className="px-1 text-[10px] text-[var(--text-muted)]">No goals</span>
        )}
        {goals.map((g) => (
          <button
            key={g.id}
            onClick={() => openGoal(g.id)}
            className="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left hover:bg-[var(--bg-subtle)]"
          >
            <GoalStatusDot status={g.status} />
            <span className="flex-1 truncate font-mono text-[11px] text-[var(--text-primary)]">
              {g.description}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}
