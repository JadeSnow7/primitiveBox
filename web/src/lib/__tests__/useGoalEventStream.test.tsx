import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useGoalEventStream } from '@/hooks/useGoalEventStream'
import { useGoalStore } from '@/store/goalStore'

const createGoalEventStream = vi.fn()

vi.mock('@/api/goalEvents', () => ({
  createGoalEventStream: (...args: unknown[]) => createGoalEventStream(...args),
}))

function HookHarness() {
  useGoalEventStream()
  return null
}

describe('useGoalEventStream', () => {
  beforeEach(() => {
    createGoalEventStream.mockReset()
    useGoalStore.setState({
      goals: [],
      selectedId: null,
      loading: false,
      error: null,
      bindings: {},
      load: vi.fn().mockResolvedValue(undefined),
      create: vi.fn(),
      select: vi.fn(),
      refresh: vi.fn().mockResolvedValue(undefined),
      replay: vi.fn(),
      execute: vi.fn(),
      loadBindings: vi.fn().mockResolvedValue(undefined),
      approve: vi.fn(),
      reject: vi.fn(),
      resume: vi.fn(),
    })
  })

  it('refreshes the goal on verification lifecycle events', async () => {
    let onEvent: ((event: { type: string; data?: Record<string, unknown> | null; message: string }) => void) | undefined
    createGoalEventStream.mockImplementation((cb) => {
      onEvent = cb
      return () => {}
    })

    const container = document.createElement('div')
    const root = createRoot(container)

    await act(async () => {
      root.render(<HookHarness />)
    })

    await act(async () => {
      onEvent?.({
        type: 'goal.verification_started',
        message: 'ignored',
        data: { goal_id: 'goal-123' },
      })
    })

    expect(useGoalStore.getState().refresh).toHaveBeenCalledWith('goal-123')

    await act(async () => {
      root.unmount()
    })
  })
})
