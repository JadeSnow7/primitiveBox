import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserApp } from '@/UserApp'
import { useSandboxStore } from '@/store/sandboxStore'
import { useUIStore } from '@/store/uiStore'
import { useGoalStore } from '@/store/goalStore'
import { listSandboxes } from '@/api/sandboxes'

vi.mock('@/api/client', () => ({
  getHealth: vi.fn().mockResolvedValue({ status: 'ok', time: '' }),
  apiFetch: vi.fn(),
  apiRequest: vi.fn(),
  APIError: class APIError extends Error {
    constructor(public status: number, message: string) { super(message) }
  },
}))

vi.mock('@/api/goals', () => ({
  listGoals: vi.fn().mockResolvedValue([]),
  createGoal: vi.fn(),
  getGoal: vi.fn(),
  executeGoal: vi.fn(),
  approveGoalReview: vi.fn(),
  rejectGoalReview: vi.fn(),
  resumeGoal: vi.fn(),
  replayGoal: vi.fn(),
  listGoalBindings: vi.fn().mockResolvedValue([]),
}))

vi.mock('@/api/goalEvents', () => ({
  createGoalEventStream: vi.fn().mockReturnValue(() => {}),
}))

vi.mock('@/api/sandboxes', () => ({
  listSandboxes: vi.fn().mockResolvedValue([]),
  getSandbox: vi.fn(),
  createSandbox: vi.fn(),
  destroySandbox: vi.fn(),
}))

function makeRunningSandbox() {
  return { id: 'sb-run', status: 'running', driver: 'docker', workspace: '/w', ttl_seconds: 300, created_at: 1, updated_at: 2 }
}

function makeStoppedSandbox() {
  return { id: 'sb-stop', status: 'stopped', driver: 'docker', workspace: '/w', ttl_seconds: 300, created_at: 1, updated_at: 2 }
}

describe('UserApp', () => {
  beforeEach(() => {
    vi.mocked(listSandboxes).mockResolvedValue([])
    useUIStore.setState({ gatewayStatus: 'checking', selectedEventId: null, detailOpen: true, createDialogOpen: false, setGatewayStatus: useUIStore.getState().setGatewayStatus, setSelectedEventId: useUIStore.getState().setSelectedEventId, setDetailOpen: useUIStore.getState().setDetailOpen, setCreateDialogOpen: useUIStore.getState().setCreateDialogOpen })
    useGoalStore.setState({ goals: [], selectedId: null, loading: false, error: null, bindings: {}, load: vi.fn().mockResolvedValue(undefined), create: vi.fn(), select: vi.fn(), refresh: vi.fn(), replay: vi.fn(), execute: vi.fn(), loadBindings: vi.fn(), approve: vi.fn(), reject: vi.fn(), resume: vi.fn() })
  })

  it('shows "服务未就绪" when no running sandbox exists after load', async () => {
    vi.mocked(listSandboxes).mockResolvedValue([makeStoppedSandbox()])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserApp />) })
    expect(container.textContent).toContain('服务未就绪')
    await act(async () => { root.unmount() })
  })

  it('selects the first running sandbox, not just the first sandbox', async () => {
    const selectMock = vi.fn()
    useSandboxStore.setState({
      sandboxes: [makeStoppedSandbox(), makeRunningSandbox()],
      selectedId: 'sb-stop',
      loading: false,
      error: null,
      capabilityNotice: null,
      load: vi.fn().mockImplementation(async () => {
        useSandboxStore.setState({ sandboxes: [makeStoppedSandbox(), makeRunningSandbox()], loading: false })
      }),
      refreshSelected: vi.fn(),
      select: selectMock,
      create: vi.fn(),
      destroy: vi.fn(),
    })
    vi.mocked(listSandboxes).mockResolvedValue([makeStoppedSandbox(), makeRunningSandbox()])

    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserApp />) })
    await act(async () => {})
    expect(selectMock).toHaveBeenCalledWith('sb-run')
    await act(async () => { root.unmount() })
  })

  it('sets gatewayStatus to online when getHealth succeeds', async () => {
    vi.mocked(listSandboxes).mockResolvedValue([makeRunningSandbox()])
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => { root.render(<UserApp />) })
    expect(useUIStore.getState().gatewayStatus).toBe('online')
    await act(async () => { root.unmount() })
  })
})
