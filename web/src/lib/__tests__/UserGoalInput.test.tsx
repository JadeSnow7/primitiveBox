import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserGoalInput } from '@/components/user/UserGoalInput'
import { useGoalStore } from '@/store/goalStore'
import { useSandboxStore } from '@/store/sandboxStore'
import type { Goal } from '@/types/goal'

function makeGoal(): Goal {
  return { id: 'new-g1', description: 'my task', status: 'created', packages: [], sandbox_ids: [], steps: [], verifications: [], created_at: 1, updated_at: 2 }
}

describe('UserGoalInput', () => {
  const createMock = vi.fn()
  const executeMock = vi.fn()
  const selectMock = vi.fn()

  beforeEach(() => {
    createMock.mockReset().mockResolvedValue(makeGoal())
    executeMock.mockReset().mockResolvedValue(undefined)
    selectMock.mockReset()

    useGoalStore.setState({
      goals: [],
      selectedId: null,
      loading: false,
      error: null,
      bindings: {},
      load: vi.fn(),
      create: createMock,
      select: selectMock,
      refresh: vi.fn(),
      replay: vi.fn(),
      execute: executeMock,
      loadBindings: vi.fn(),
      approve: vi.fn(),
      reject: vi.fn(),
      resume: vi.fn(),
    })

    useSandboxStore.setState({
      sandboxes: [],
      selectedId: 'sandbox-1',
      loading: false,
      error: null,
      capabilityNotice: null,
      load: vi.fn(),
      refreshSelected: vi.fn(),
      select: vi.fn(),
      create: vi.fn(),
      destroy: vi.fn(),
    })
  })

  it('calls create then execute in sequence on submit', async () => {
    const onClose = vi.fn()
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalInput onClose={onClose} />)
    })

    const textarea = container.querySelector('textarea')!
    const nativeInputSetter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!
    nativeInputSetter.call(textarea, 'analyze data')
    await act(async () => {
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const form = container.querySelector('form')!
    await act(async () => {
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })

    expect(createMock).toHaveBeenCalledWith({
      description: 'analyze data',
      packages: [],
      sandbox_ids: ['sandbox-1'],
    })
    expect(executeMock).toHaveBeenCalledWith('new-g1')
    expect(selectMock).toHaveBeenCalledWith('new-g1')
    expect(onClose).toHaveBeenCalledOnce()
    await act(async () => { root.unmount() })
  })

  it('shows error message when create fails', async () => {
    createMock.mockRejectedValue(new Error('network error'))
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<UserGoalInput onClose={vi.fn()} />)
    })

    const nativeInputSetter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!
    const textarea = container.querySelector('textarea')!
    nativeInputSetter.call(textarea, 'some task')
    await act(async () => {
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const form = container.querySelector('form')!
    await act(async () => {
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })

    expect(container.textContent).toContain('任务创建失败')
    await act(async () => { root.unmount() })
  })
})
