import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserExecutionView } from '@/components/user/UserExecutionView'
import { useGoalStore } from '@/store/goalStore'
import type { Goal, GoalStep, GoalReview } from '@/types/goal'

function makeStep(seq: number, status: GoalStep['status'], primitive = 'fs.read', input: Record<string, unknown> = { path: `/f${seq}.txt` }): GoalStep {
  return { id: `s${seq}`, goal_id: 'g1', primitive, input, status, seq, created_at: 1, updated_at: 2 }
}

function makeReview(status: GoalReview['status']): GoalReview {
  return { id: 'rev-1', goal_id: 'g1', step_id: 's1', status, primitive: 'fs.write', risk_level: 'medium', reversible: true, side_effect: '写入报告', created_at: 1, updated_at: 2 }
}

function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return { id: 'g1', description: '测试目标', status: 'executing', packages: [], sandbox_ids: [], steps: [], verifications: [], created_at: 1, updated_at: 2, ...overrides }
}

function resetStore(goal: Goal | null) {
  useGoalStore.setState({
    goals: goal ? [goal] : [],
    selectedId: goal ? goal.id : null,
    loading: false, error: null, bindings: {},
    load: vi.fn(), create: vi.fn(), select: vi.fn(), refresh: vi.fn(),
    replay: vi.fn(), execute: vi.fn(), loadBindings: vi.fn(),
    approve: vi.fn().mockResolvedValue(undefined),
    reject: vi.fn().mockResolvedValue(undefined),
    resume: vi.fn().mockResolvedValue(undefined),
  })
}

describe('UserExecutionView', () => {
  beforeEach(() => resetStore(null))

  it('shows empty state when no goal selected', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).toContain('选择一个任务')
    await act(async () => { root.unmount() })
  })

  it('renders goal description', async () => {
    resetStore(makeGoal())
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).toContain('测试目标')
    await act(async () => { root.unmount() })
  })

  it('renders steps sorted by seq with human-readable labels', async () => {
    resetStore(makeGoal({ steps: [makeStep(2, 'pending'), makeStep(1, 'passed')] }))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    const text = container.textContent ?? ''
    expect(text).toContain('读取文件 /f1.txt')
    expect(text).toContain('读取文件 /f2.txt')
    expect(text.indexOf('f1')).toBeLessThan(text.indexOf('f2'))
    await act(async () => { root.unmount() })
  })

  it('shows ApprovalCard when goal is paused and pending review exists', async () => {
    resetStore(makeGoal({ status: 'paused', reviews: [makeReview('pending')] }))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).toContain('AI 需要你确认')
    expect(container.textContent).toContain('写入报告')
    await act(async () => { root.unmount() })
  })

  it('does not show ApprovalCard when review is already approved', async () => {
    resetStore(makeGoal({ status: 'executing', reviews: [makeReview('approved')] }))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).not.toContain('AI 需要你确认')
    await act(async () => { root.unmount() })
  })

  it('shows 已完成 banner when goal status is completed', async () => {
    resetStore(makeGoal({ status: 'completed' }))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).toContain('任务已完成')
    await act(async () => { root.unmount() })
  })

  it('shows error banner when goal status is failed', async () => {
    resetStore(makeGoal({ status: 'failed' }))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserExecutionView />) })
    expect(container.textContent).toContain('执行过程中出现错误')
    await act(async () => { root.unmount() })
  })
})
