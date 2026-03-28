import { create } from 'zustand'
import {
  approveGoalReview as approveGoalReviewRequest,
  createGoal as createGoalRequest,
  executeGoal as executeGoalRequest,
  getGoal,
  listGoalBindings,
  listGoals,
  rejectGoalReview as rejectGoalReviewRequest,
  replayGoal as replayGoalRequest,
  resumeGoal as resumeGoalRequest,
} from '@/api/goals'
import type { Goal, GoalBinding, GoalReplayResult } from '@/types/goal'

interface GoalState {
  goals: Goal[]
  selectedId: string | null
  loading: boolean
  error: string | null
  bindings: Record<string, GoalBinding[]>

  load: () => Promise<void>
  create: (params: { description: string; packages: string[]; sandbox_ids: string[] }) => Promise<Goal>
  select: (id: string | null) => void
  refresh: (id: string) => Promise<void>
  replay: (id: string, mode?: 'full' | 'skip_passed' | 'step_by_step') => Promise<GoalReplayResult>
  execute: (id: string) => Promise<void>
  loadBindings: (goalId: string) => Promise<void>
  approve: (goalId: string, reviewId: string) => Promise<void>
  reject: (goalId: string, reviewId: string, reason?: string) => Promise<void>
  resume: (goalId: string) => Promise<void>
}

export const useGoalStore = create<GoalState>((set, get) => ({
  goals: [],
  selectedId: null,
  loading: false,
  error: null,
  bindings: {},

  load: async () => {
    set({ loading: true, error: null })
    try {
      const goals = await listGoals()
      set({ goals, loading: false })
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : 'Failed to load goals' })
    }
  },

  create: async (params) => {
    const goal = await createGoalRequest(params)
    set((s) => ({ goals: [goal, ...s.goals], selectedId: goal.id }))
    return goal
  },

  select: (id) => set({ selectedId: id }),

  refresh: async (id) => {
    try {
      const updated = await getGoal(id)
      set((s) => ({
        goals: s.goals.map((g) => (g.id === id ? updated : g)),
      }))
    } catch {
      // Keep current state on refresh failure.
    }
  },

  replay: async (id, mode = 'full') => {
    return replayGoalRequest(id, mode)
  },

  execute: async (id) => {
    await executeGoalRequest(id)
    // Refresh the goal to pick up the new 'executing' status.
    await get().refresh(id)
  },

  loadBindings: async (goalId) => {
    const bindings = await listGoalBindings(goalId)
    set((s) => ({ bindings: { ...s.bindings, [goalId]: bindings } }))
  },

  approve: async (goalId, reviewId) => {
    await approveGoalReviewRequest(goalId, reviewId)
    await get().refresh(goalId)
  },

  reject: async (goalId, reviewId, reason) => {
    await rejectGoalReviewRequest(goalId, reviewId, reason)
    await get().refresh(goalId)
  },

  resume: async (goalId) => {
    await resumeGoalRequest(goalId)
    await get().refresh(goalId)
  },
}))

/** Non-reactive snapshot of the currently selected goal. */
export function getSelectedGoal(): Goal | undefined {
  const { goals, selectedId } = useGoalStore.getState()
  return selectedId ? goals.find((g) => g.id === selectedId) : undefined
}

/** Non-reactive snapshot of resolved bindings for a goal. */
export function getResolvedBindings(goalId: string): GoalBinding[] {
  const { bindings } = useGoalStore.getState()
  return (bindings[goalId] ?? []).filter((b) => b.status === 'resolved')
}
