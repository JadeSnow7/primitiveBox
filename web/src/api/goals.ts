import { apiFetch } from '@/api/client'
import type { Goal, GoalBinding, GoalReplayResult } from '@/types/goal'

export async function approveGoalReview(
  goalId: string,
  reviewId: string,
): Promise<{ goal_id: string; review_id: string; status: string }> {
  return apiFetch(`/api/v1/goals/${goalId}/approve`, {
    method: 'POST',
    body: JSON.stringify({ review_id: reviewId }),
  })
}

export async function rejectGoalReview(
  goalId: string,
  reviewId: string,
  reason = '',
): Promise<{ goal_id: string; review_id: string; status: string }> {
  return apiFetch(`/api/v1/goals/${goalId}/reject`, {
    method: 'POST',
    body: JSON.stringify({ review_id: reviewId, reason }),
  })
}

export async function resumeGoal(goalId: string): Promise<{ goal_id: string; status: string }> {
  return apiFetch(`/api/v1/goals/${goalId}/resume`, {
    method: 'POST',
  })
}

export async function listGoals(): Promise<Goal[]> {
  const data = await apiFetch<{ goals: Goal[] }>('/api/v1/goals')
  return data.goals ?? []
}

export async function createGoal(params: {
  description: string
  packages: string[]
  sandbox_ids: string[]
}): Promise<Goal> {
  return apiFetch<Goal>('/api/v1/goals', {
    method: 'POST',
    body: JSON.stringify(params),
  })
}

export async function getGoal(id: string): Promise<Goal> {
  return apiFetch<Goal>(`/api/v1/goals/${id}`)
}

export async function replayGoal(
  id: string,
  mode: 'full' | 'skip_passed' | 'step_by_step' = 'full',
): Promise<GoalReplayResult> {
  return apiFetch<GoalReplayResult>(`/api/v1/goals/${id}/replay`, {
    method: 'POST',
    body: JSON.stringify({ mode }),
  })
}

export async function executeGoal(id: string): Promise<{ goal_id: string; status: string }> {
  return apiFetch<{ goal_id: string; status: string }>(`/api/v1/goals/${id}/execute`, {
    method: 'POST',
  })
}

export async function listGoalBindings(goalId: string): Promise<GoalBinding[]> {
  const data = await apiFetch<{ goal_id: string; bindings: GoalBinding[] }>(
    `/api/v1/goals/${goalId}/bindings`,
  )
  return data.bindings ?? []
}
