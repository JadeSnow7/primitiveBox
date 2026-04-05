import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserApprovalCard } from '@/components/user/UserApprovalCard'
import { useGoalStore } from '@/store/goalStore'
import type { Goal, GoalReview } from '@/types/goal'

function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: 'goal-1',
    description: 'Test goal',
    status: 'paused',
    packages: [],
    sandbox_ids: [],
    steps: [],
    verifications: [],
    reviews: [],
    created_at: 1,
    updated_at: 2,
    ...overrides,
  }
}

function makeReview(overrides: Partial<GoalReview> = {}): GoalReview {
  return {
    id: 'review-1',
    goal_id: 'goal-1',
    step_id: 'step-1',
    status: 'pending',
    primitive: 'fs.write',
    risk_level: 'medium',
    reversible: true,
    side_effect: '写入文件 /out/report.pdf',
    created_at: 1,
    updated_at: 2,
    ...overrides,
  }
}

describe('UserApprovalCard', () => {
  const approveMock = vi.fn().mockResolvedValue(undefined)
  const resumeMock = vi.fn().mockResolvedValue(undefined)
  const rejectMock = vi.fn().mockResolvedValue(undefined)

  beforeEach(() => {
    approveMock.mockClear()
    resumeMock.mockClear()
    rejectMock.mockClear()
    useGoalStore.setState({
      goals: [],
      selectedId: null,
      loading: false,
      error: null,
      bindings: {},
      load: vi.fn(),
      create: vi.fn(),
      select: vi.fn(),
      refresh: vi.fn(),
      replay: vi.fn(),
      execute: vi.fn(),
      loadBindings: vi.fn(),
      approve: approveMock,
      reject: rejectMock,
      resume: resumeMock,
    })
  })

  it('renders side_effect text', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview()} />)
    })
    expect(container.textContent).toContain('写入文件 /out/report.pdf')
    await act(async () => { root.unmount() })
  })

  it('shows "不可撤销" warning when reversible is false', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview({ reversible: false })} />)
    })
    expect(container.textContent).toContain('不可撤销')
    await act(async () => { root.unmount() })
  })

  it('does not show "不可撤销" when reversible is true', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview({ reversible: true })} />)
    })
    expect(container.textContent).not.toContain('不可撤销')
    await act(async () => { root.unmount() })
  })

  it('calls approve then resume when 批准 is clicked', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview()} />)
    })
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent?.includes('批准')
    )!
    await act(async () => { btn.click() })
    expect(approveMock).toHaveBeenCalledWith('goal-1', 'review-1')
    expect(resumeMock).toHaveBeenCalledWith('goal-1')
    await act(async () => { root.unmount() })
  })

  it('calls reject when 拒绝 is clicked', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview()} />)
    })
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent?.includes('拒绝')
    )!
    await act(async () => { btn.click() })
    expect(rejectMock).toHaveBeenCalledWith('goal-1', 'review-1', undefined)
    await act(async () => { root.unmount() })
  })

  it('shows error message when approve throws', async () => {
    approveMock.mockRejectedValueOnce(new Error('network error'))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserApprovalCard goal={makeGoal()} review={makeReview()} />)
    })
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent?.includes('批准')
    )!
    await act(async () => { btn.click() })
    expect(container.textContent).toContain('操作失败')
    await act(async () => { root.unmount() })
  })
})
