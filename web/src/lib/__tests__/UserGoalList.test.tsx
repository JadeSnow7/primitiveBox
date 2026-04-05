import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserGoalList } from '@/components/user/UserGoalList'
import { useGoalStore } from '@/store/goalStore'
import type { Goal } from '@/types/goal'

function makeGoal(id: string, status: Goal['status'], description: string): Goal {
  return {
    id,
    description,
    status,
    packages: [],
    sandbox_ids: [],
    steps: [],
    verifications: [],
    created_at: 1,
    updated_at: 2,
  }
}

function resetStore(goals: Goal[], selectedId: string | null = null) {
  useGoalStore.setState({
    goals,
    selectedId,
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
    approve: vi.fn(),
    reject: vi.fn(),
    resume: vi.fn(),
  })
}

describe('UserGoalList', () => {
  beforeEach(() => resetStore([]))

  it('renders goal descriptions', async () => {
    resetStore([makeGoal('g1', 'completed', '分析销售数据')])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    expect(container.textContent).toContain('分析销售数据')
    await act(async () => { root.unmount() })
  })

  it('shows "已完成" badge for completed goals', async () => {
    resetStore([makeGoal('g1', 'completed', 'done task')])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    expect(container.textContent).toContain('已完成')
    await act(async () => { root.unmount() })
  })

  it('shows "待确认" badge for paused goals', async () => {
    resetStore([makeGoal('g1', 'paused', 'paused task')])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    expect(container.textContent).toContain('待确认')
    await act(async () => { root.unmount() })
  })

  it('shows "执行中" badge for executing goals', async () => {
    resetStore([makeGoal('g1', 'executing', 'running task')])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    expect(container.textContent).toContain('执行中')
    await act(async () => { root.unmount() })
  })

  it('calls select(id) when a goal is clicked', async () => {
    const selectMock = vi.fn()
    resetStore([makeGoal('g1', 'completed', 'click me')])
    useGoalStore.setState({ select: selectMock })

    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    const btn = container.querySelector('button[data-goal-id="g1"]') as HTMLButtonElement
    await act(async () => { btn.click() })
    expect(selectMock).toHaveBeenCalledWith('g1')
    await act(async () => { root.unmount() })
  })

  it('shows empty state when there are no goals', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={vi.fn()} />)
    })
    expect(container.textContent).toContain('还没有任务')
    await act(async () => { root.unmount() })
  })

  it('calls onNewGoal when + 新建任务 is clicked', async () => {
    const onNewGoal = vi.fn()
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalList onNewGoal={onNewGoal} />)
    })
    const btn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent?.includes('新建任务')
    )!
    await act(async () => { btn.click() })
    expect(onNewGoal).toHaveBeenCalledOnce()
    await act(async () => { root.unmount() })
  })
})
