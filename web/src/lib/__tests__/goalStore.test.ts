/**
 * goalStore.test.ts
 *
 * Verifies that:
 *   - load() populates the goals array from the API
 *   - create() adds the new goal and sets selectedId
 *   - select() updates selectedId
 *   - refresh() updates the matching goal in the list
 *   - replay() returns GoalReplayResult
 *   - loadBindings() populates bindings[goalId]
 *   - getResolvedBindings() filters to resolved only
 *   - getSelectedGoal() returns undefined when no selection
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useGoalStore, getSelectedGoal, getResolvedBindings } from '@/store/goalStore'
import type { Goal, GoalReplayResult } from '@/types/goal'

vi.mock('@/api/goals', () => ({
  listGoals: vi.fn(),
  createGoal: vi.fn(),
  getGoal: vi.fn(),
  replayGoal: vi.fn(),
  listGoalBindings: vi.fn(),
}))

import {
  listGoals,
  createGoal as createGoalApi,
  getGoal,
  replayGoal as replayGoalApi,
  listGoalBindings,
} from '@/api/goals'

const mockedListGoals = listGoals as ReturnType<typeof vi.fn>
const mockedCreateGoal = createGoalApi as ReturnType<typeof vi.fn>
const mockedGetGoal = getGoal as ReturnType<typeof vi.fn>
const mockedReplayGoal = replayGoalApi as ReturnType<typeof vi.fn>
const mockedListGoalBindings = listGoalBindings as ReturnType<typeof vi.fn>

function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: 'goal-test-1',
    description: 'Test goal',
    status: 'created',
    packages: [],
    sandbox_ids: [],
    steps: [],
    verifications: [],
    created_at: 1711612800000,
    updated_at: 1711612800000,
    ...overrides,
  }
}

beforeEach(() => {
  useGoalStore.setState({ goals: [], selectedId: null, loading: false, error: null, bindings: {} })
  vi.clearAllMocks()
})

describe('goalStore', () => {
  it('load populates goals array', async () => {
    const goals = [makeGoal(), makeGoal({ id: 'goal-2', description: 'Another' })]
    mockedListGoals.mockResolvedValue(goals)

    await useGoalStore.getState().load()

    expect(useGoalStore.getState().goals).toHaveLength(2)
    expect(useGoalStore.getState().goals[0].id).toBe('goal-test-1')
    expect(useGoalStore.getState().loading).toBe(false)
  })

  it('create adds goal to list and sets selectedId', async () => {
    const newGoal = makeGoal({ id: 'goal-new', description: 'Created goal' })
    mockedCreateGoal.mockResolvedValue(newGoal)

    const result = await useGoalStore.getState().create({
      description: 'Created goal',
      packages: [],
      sandbox_ids: [],
    })

    expect(result.id).toBe('goal-new')
    expect(useGoalStore.getState().goals).toHaveLength(1)
    expect(useGoalStore.getState().selectedId).toBe('goal-new')
  })

  it('select updates selectedId', () => {
    useGoalStore.setState({ goals: [makeGoal()], selectedId: null })
    useGoalStore.getState().select('goal-test-1')
    expect(useGoalStore.getState().selectedId).toBe('goal-test-1')
  })

  it('select(null) clears selectedId', () => {
    useGoalStore.setState({ goals: [makeGoal()], selectedId: 'goal-test-1' })
    useGoalStore.getState().select(null)
    expect(useGoalStore.getState().selectedId).toBeNull()
  })

  it('refresh updates matching goal in list', async () => {
    const original = makeGoal()
    useGoalStore.setState({ goals: [original], selectedId: original.id })
    const updated = makeGoal({ status: 'completed' })
    mockedGetGoal.mockResolvedValue(updated)

    await useGoalStore.getState().refresh(original.id)

    const state = useGoalStore.getState()
    expect(state.goals[0].status).toBe('completed')
  })

  it('replay returns GoalReplayResult', async () => {
    const replayResult: GoalReplayResult = {
      goal_id: 'goal-test-1',
      mode: 'full',
      entries: [],
    }
    mockedReplayGoal.mockResolvedValue(replayResult)

    const result = await useGoalStore.getState().replay('goal-test-1', 'full')

    expect(result.goal_id).toBe('goal-test-1')
    expect(result.mode).toBe('full')
    expect(mockedReplayGoal).toHaveBeenCalledWith('goal-test-1', 'full')
  })

  it('loadBindings populates bindings[goalId]', async () => {
    const bindings = [
      { id: 'bind-1', goal_id: 'goal-1', binding_type: 'service_endpoint' as const, source_ref: 'postgres:5432', target_ref: 'env.DATABASE_URL', status: 'pending' as const, created_at: 1, updated_at: 1 },
    ]
    mockedListGoalBindings.mockResolvedValue(bindings)

    await useGoalStore.getState().loadBindings('goal-1')

    const state = useGoalStore.getState()
    expect(state.bindings['goal-1']).toHaveLength(1)
    expect(state.bindings['goal-1'][0].source_ref).toBe('postgres:5432')
  })

  it('loadBindings for different goals do not overwrite each other', async () => {
    mockedListGoalBindings
      .mockResolvedValueOnce([{ id: 'b1', goal_id: 'g1', binding_type: 'credential' as const, source_ref: 'a', target_ref: 'b', status: 'pending' as const, created_at: 1, updated_at: 1 }])
      .mockResolvedValueOnce([{ id: 'b2', goal_id: 'g2', binding_type: 'network_exposure' as const, source_ref: 'c', target_ref: 'd', status: 'pending' as const, created_at: 1, updated_at: 1 }])

    await useGoalStore.getState().loadBindings('g1')
    await useGoalStore.getState().loadBindings('g2')

    const state = useGoalStore.getState()
    expect(state.bindings['g1']).toHaveLength(1)
    expect(state.bindings['g2']).toHaveLength(1)
  })

  it('getResolvedBindings filters to resolved only', async () => {
    const bindings = [
      { id: 'b1', goal_id: 'goal-1', binding_type: 'service_endpoint' as const, source_ref: 'postgres:5432', target_ref: 'env.DATABASE_URL', status: 'pending' as const, created_at: 1, updated_at: 1 },
      { id: 'b2', goal_id: 'goal-1', binding_type: 'service_endpoint' as const, source_ref: 'nginx:80', target_ref: 'app:8080', status: 'resolved' as const, resolved_value: 'http://nginx', created_at: 1, updated_at: 2 },
    ]
    mockedListGoalBindings.mockResolvedValue(bindings)
    await useGoalStore.getState().loadBindings('goal-1')

    const resolved = getResolvedBindings('goal-1')
    expect(resolved).toHaveLength(1)
    expect(resolved[0].source_ref).toBe('nginx:80')
  })

  it('getSelectedGoal returns undefined when no selection', () => {
    expect(getSelectedGoal()).toBeUndefined()
  })

  it('getSelectedGoal returns the selected goal', async () => {
    const goal = makeGoal()
    mockedListGoals.mockResolvedValue([goal])
    await useGoalStore.getState().load()
    useGoalStore.getState().select(goal.id)

    expect(getSelectedGoal()?.id).toBe(goal.id)
  })
})
