import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { GoalPanel } from '@/components/workspace/panels/GoalPanel'
import { useGoalStore } from '@/store/goalStore'
import type { Goal } from '@/types/goal'
import type { WorkspacePanel } from '@/types/workspace'

function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: 'goal-panel-1',
    description: 'Verification-heavy goal',
    status: 'verifying',
    packages: [],
    sandbox_ids: [],
    steps: [],
    verifications: [
      {
        id: 'verify-running',
        goal_id: 'goal-panel-1',
        status: 'running',
        verdict: 'probing endpoint',
        check_type: 'http_probe',
        evidence: { body: 'healthy' },
        created_at: 1,
        updated_at: 2,
      },
      {
        id: 'verify-failed',
        goal_id: 'goal-panel-1',
        status: 'failed',
        verdict: 'expected HTTP 200, got 500',
        check_type: 'http_probe',
        evidence: { observed_status: 500 },
        created_at: 3,
        updated_at: 4,
      },
    ],
    created_at: 1,
    updated_at: 2,
    ...overrides,
  }
}

describe('GoalPanel', () => {
  it('renders verifying status and verification truth details', async () => {
    useGoalStore.setState({
      goals: [makeGoal()],
      selectedId: 'goal-panel-1',
      loading: false,
      error: null,
      bindings: {},
      load: async () => {},
      create: async () => makeGoal(),
      select: () => {},
      refresh: async () => {},
      replay: async () => ({ goal_id: 'goal-panel-1', mode: 'full', entries: [] }),
      execute: async () => {},
      loadBindings: async () => {},
      approve: async () => {},
      reject: async () => {},
      resume: async () => {},
    })

    const panel: WorkspacePanel = {
      id: 'panel-goal',
      type: 'goal',
      props: { goalId: 'goal-panel-1' },
    }

    const container = document.createElement('div')
    const root = createRoot(container)

    await act(async () => {
      root.render(<GoalPanel panel={panel} />)
    })

    expect(container.textContent).toContain('verifying')
    expect(container.textContent).toContain('http_probe')
    expect(container.textContent).toContain('expected HTTP 200, got 500')

    await act(async () => {
      root.unmount()
    })
  })
})
