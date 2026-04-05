# User Frontend (/app) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `/app` route that gives non-technical end users a clean, Chinese-language interface to create goals, watch AI execution progress, and approve/reject actions — without any developer-facing concepts.

**Architecture:** `main.tsx` checks `window.location.pathname` at runtime and renders either the existing `<App />` (developer UI) or a new `<UserApp />` (user UI). The user UI reads persistent goal state from `goalStore` (not `timelineStore`) and uses `useGoalEventStream` for real-time SSE updates. No backend changes required.

**Tech Stack:** React 18, TypeScript, Zustand, Tailwind CSS (CSS vars from existing theme), Vitest + jsdom

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Modify | `web/src/main.tsx` | Add `/app` pathname check → render `<UserApp />` |
| Create | `web/src/UserApp.tsx` | Root: health check, load stores, sandbox auto-select, render `UserShell` |
| Create | `web/src/components/user/UserShell.tsx` | Layout: topbar + left/right panels |
| Create | `web/src/components/user/UserGoalList.tsx` | Left panel: goal list with status badges + "新建任务" button |
| Create | `web/src/components/user/UserGoalInput.tsx` | New-goal form: textarea, create→execute sequence |
| Create | `web/src/components/user/UserExecutionView.tsx` | Right panel: sorted `goal.steps`, pending ApprovalCard |
| Create | `web/src/components/user/UserApprovalCard.tsx` | Approve/reject a `GoalReview`: calls `approve + resume` or `reject` |
| Create | `web/src/lib/stepFormatter.ts` | Pure fn: `GoalStep → human-readable Chinese label` |
| Create | `web/src/lib/__tests__/stepFormatter.test.ts` | Unit tests for stepFormatter |
| Create | `web/src/lib/__tests__/UserApprovalCard.test.tsx` | Component test for ApprovalCard |
| Create | `web/src/lib/__tests__/UserGoalList.test.tsx` | Component test for GoalList |
| Create | `web/src/lib/__tests__/UserGoalInput.test.tsx` | Component test for GoalInput |
| Create | `web/src/lib/__tests__/UserExecutionView.test.tsx` | Component test for ExecutionView |
| Create | `web/src/lib/__tests__/UserApp.test.tsx` | Integration test for UserApp mount |

---

## Task 1: stepFormatter utility

**Files:**
- Create: `web/src/lib/stepFormatter.ts`
- Create: `web/src/lib/__tests__/stepFormatter.test.ts`

- [ ] **Step 1: Write the failing tests**

```ts
// web/src/lib/__tests__/stepFormatter.test.ts
import { describe, it, expect } from 'vitest'
import { formatStepLabel } from '@/lib/stepFormatter'
import type { GoalStep } from '@/types/goal'

function makeStep(overrides: Partial<GoalStep>): GoalStep {
  return {
    id: 's1',
    goal_id: 'g1',
    primitive: 'fs.read',
    input: {},
    status: 'pending',
    seq: 0,
    created_at: 1,
    updated_at: 1,
    ...overrides,
  }
}

describe('formatStepLabel', () => {
  it('maps fs.read with path param to Chinese label', () => {
    const step = makeStep({ primitive: 'fs.read', input: { path: '/data/sales.csv' } })
    expect(formatStepLabel(step)).toBe('读取文件 /data/sales.csv')
  })

  it('maps fs.write with path param', () => {
    const step = makeStep({ primitive: 'fs.write', input: { path: '/out/report.pdf' } })
    expect(formatStepLabel(step)).toBe('写入文件 /out/report.pdf')
  })

  it('maps shell.exec with command param', () => {
    const step = makeStep({ primitive: 'shell.exec', input: { command: 'npm test' } })
    expect(formatStepLabel(step)).toBe('执行命令 npm test')
  })

  it('maps http.fetch with url param', () => {
    const step = makeStep({ primitive: 'http.fetch', input: { url: 'https://api.example.com' } })
    expect(formatStepLabel(step)).toBe('请求网络 https://api.example.com')
  })

  it('uses primitive name as fallback for unknown primitives', () => {
    const step = makeStep({ primitive: 'custom.op', input: {} })
    expect(formatStepLabel(step)).toBe('custom.op')
  })

  it('omits param when key param is missing from input', () => {
    const step = makeStep({ primitive: 'fs.read', input: {} })
    expect(formatStepLabel(step)).toBe('读取文件')
  })

  it('maps fs.list with path param', () => {
    const step = makeStep({ primitive: 'fs.list', input: { path: '/src' } })
    expect(formatStepLabel(step)).toBe('列出目录 /src')
  })

  it('maps fs.delete with path param', () => {
    const step = makeStep({ primitive: 'fs.delete', input: { path: '/tmp/old.txt' } })
    expect(formatStepLabel(step)).toBe('删除文件 /tmp/old.txt')
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/stepFormatter.test.ts
```

Expected: FAIL — `Cannot find module '@/lib/stepFormatter'`

- [ ] **Step 3: Write the implementation**

```ts
// web/src/lib/stepFormatter.ts
import type { GoalStep } from '@/types/goal'

const PRIMITIVE_LABELS: Record<string, string> = {
  'fs.read':    '读取文件',
  'fs.write':   '写入文件',
  'fs.list':    '列出目录',
  'fs.delete':  '删除文件',
  'shell.exec': '执行命令',
  'http.fetch': '请求网络',
  'http.post':  '发送请求',
}

const KEY_PARAMS: Record<string, string> = {
  'fs.read':    'path',
  'fs.write':   'path',
  'fs.list':    'path',
  'fs.delete':  'path',
  'shell.exec': 'command',
  'http.fetch': 'url',
  'http.post':  'url',
}

export function formatStepLabel(step: GoalStep): string {
  const verb = PRIMITIVE_LABELS[step.primitive] ?? step.primitive
  const keyParam = KEY_PARAMS[step.primitive]
  const paramValue = keyParam !== undefined ? step.input[keyParam] : undefined
  return paramValue !== undefined ? `${verb} ${String(paramValue)}` : verb
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/stepFormatter.test.ts
```

Expected: 8 tests pass.

- [ ] **Step 5: Commit**

```bash
cd web && git add src/lib/stepFormatter.ts src/lib/__tests__/stepFormatter.test.ts
git commit -m "feat(user-ui): add stepFormatter for GoalStep → Chinese label"
```

---

## Task 2: UserApprovalCard component

**Files:**
- Create: `web/src/components/user/UserApprovalCard.tsx`
- Create: `web/src/lib/__tests__/UserApprovalCard.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
// web/src/lib/__tests__/UserApprovalCard.test.tsx
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
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/UserApprovalCard.test.tsx
```

Expected: FAIL — `Cannot find module '@/components/user/UserApprovalCard'`

- [ ] **Step 3: Create the component directory and file**

```tsx
// web/src/components/user/UserApprovalCard.tsx
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { useGoalStore } from '@/store/goalStore'
import type { Goal, GoalReview } from '@/types/goal'

const BORDER_COLOR: Record<string, string> = {
  low:    'border-[var(--border)]',
  medium: 'border-orange-400',
  high:   'border-red-500',
}

export function UserApprovalCard({ goal, review }: { goal: Goal; review: GoalReview }) {
  const approve = useGoalStore((s) => s.approve)
  const resume  = useGoalStore((s) => s.resume)
  const reject  = useGoalStore((s) => s.reject)

  const [loading, setLoading] = useState<'approve' | 'reject' | null>(null)
  const [error, setError]     = useState<string | null>(null)

  const borderColor = BORDER_COLOR[review.risk_level] ?? 'border-orange-400'

  async function handleApprove() {
    setLoading('approve')
    setError(null)
    try {
      await approve(goal.id, review.id)
      await resume(goal.id)
    } catch {
      setError('操作失败，请重试')
    } finally {
      setLoading(null)
    }
  }

  async function handleReject() {
    setLoading('reject')
    setError(null)
    try {
      await reject(goal.id, review.id, undefined)
    } catch {
      setError('操作失败，请重试')
    } finally {
      setLoading(null)
    }
  }

  return (
    <div className={`rounded-lg border ${borderColor} bg-[var(--bg-raised)] p-4`}>
      <div className="mb-2 text-[12px] font-semibold text-orange-400">⏸ AI 需要你确认后继续</div>
      <div className="mb-1 text-[12px] text-[var(--text-primary)]">即将执行：</div>
      <div className="mb-3 border-l-2 border-[var(--border)] pl-3 text-[12px] text-[var(--text-secondary)]">
        {review.side_effect ?? review.primitive}
      </div>
      {!review.reversible && (
        <div className="mb-3 text-[11px] text-red-400">此操作不可撤销</div>
      )}
      {error !== null && (
        <div className="mb-2 text-[11px] text-red-400">{error}</div>
      )}
      <div className="flex gap-2">
        <Button
          size="sm"
          className="flex-1 border-[var(--green)] bg-[var(--green-bg)] text-[var(--green)] hover:opacity-80"
          disabled={loading !== null}
          onClick={() => void handleApprove()}
        >
          {loading === 'approve' ? '处理中…' : '批准'}
        </Button>
        <Button
          size="sm"
          variant="subtle"
          className="flex-1"
          disabled={loading !== null}
          onClick={() => void handleReject()}
        >
          {loading === 'reject' ? '处理中…' : '拒绝'}
        </Button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/UserApprovalCard.test.tsx
```

Expected: 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/user/UserApprovalCard.tsx web/src/lib/__tests__/UserApprovalCard.test.tsx
git commit -m "feat(user-ui): add UserApprovalCard with approve+resume / reject flow"
```

---

## Task 3: UserGoalList component

**Files:**
- Create: `web/src/components/user/UserGoalList.tsx`
- Create: `web/src/lib/__tests__/UserGoalList.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
// web/src/lib/__tests__/UserGoalList.test.tsx
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/UserGoalList.test.tsx
```

Expected: FAIL — `Cannot find module '@/components/user/UserGoalList'`

- [ ] **Step 3: Write the implementation**

```tsx
// web/src/components/user/UserGoalList.tsx
import { useGoalStore } from '@/store/goalStore'
import { Button } from '@/components/ui/button'
import type { GoalStatus } from '@/types/goal'

const STATUS_BADGE: Record<GoalStatus, { label: string; className: string }> = {
  created:   { label: '未开始', className: 'text-[var(--text-muted)]' },
  executing: { label: '执行中', className: 'text-blue-400' },
  verifying: { label: '执行中', className: 'text-blue-400' },
  paused:    { label: '待确认', className: 'text-orange-400' },
  completed: { label: '已完成', className: 'text-[var(--green,#4ade80)]' },
  failed:    { label: '失败',   className: 'text-red-400' },
}

export function UserGoalList({ onNewGoal }: { onNewGoal: () => void }) {
  const goals     = useGoalStore((s) => s.goals)
  const selectedId = useGoalStore((s) => s.selectedId)
  const select    = useGoalStore((s) => s.select)

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-[var(--border)] p-3">
        <Button size="sm" className="w-full" onClick={onNewGoal}>
          + 新建任务
        </Button>
      </div>
      <div className="flex-1 space-y-1 overflow-y-auto p-2">
        {goals.map((goal) => {
          const badge = STATUS_BADGE[goal.status] ?? { label: goal.status, className: 'text-[var(--text-muted)]' }
          const isSelected = goal.id === selectedId
          return (
            <button
              key={goal.id}
              data-goal-id={goal.id}
              onClick={() => select(goal.id)}
              className={`w-full rounded-lg border px-3 py-2.5 text-left transition-colors ${
                isSelected
                  ? 'border-[var(--blue)] bg-[var(--blue-bg)]'
                  : 'border-[var(--border)] bg-[var(--bg-raised)] hover:bg-[var(--bg-subtle)]'
              }`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-[12px] font-medium text-[var(--text-primary)]">
                  {goal.description}
                </span>
                <span className={`flex-shrink-0 text-[10px] ${badge.className}`}>
                  {badge.label}
                </span>
              </div>
            </button>
          )
        })}
        {goals.length === 0 && (
          <div className="py-8 text-center text-[12px] text-[var(--text-muted)]">
            还没有任务
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/UserGoalList.test.tsx
```

Expected: 7 tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/user/UserGoalList.tsx web/src/lib/__tests__/UserGoalList.test.tsx
git commit -m "feat(user-ui): add UserGoalList with status badges"
```

---

## Task 4: UserGoalInput component

**Files:**
- Create: `web/src/components/user/UserGoalInput.tsx`
- Create: `web/src/lib/__tests__/UserGoalInput.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
// web/src/lib/__tests__/UserGoalInput.test.tsx
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
    await act(async () => {
      textarea.value = 'analyze data'
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    // Use React's onChange — simulate input event via Object.getOwnPropertyDescriptor
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/UserGoalInput.test.tsx
```

Expected: FAIL — `Cannot find module '@/components/user/UserGoalInput'`

- [ ] **Step 3: Write the implementation**

```tsx
// web/src/components/user/UserGoalInput.tsx
import { useState } from 'react'
import { useGoalStore } from '@/store/goalStore'
import { useSandboxStore } from '@/store/sandboxStore'
import { Button } from '@/components/ui/button'

export function UserGoalInput({ onClose }: { onClose: () => void }) {
  const [description, setDescription] = useState('')
  const [submitting, setSubmitting]   = useState(false)
  const [error, setError]             = useState<string | null>(null)

  const create            = useGoalStore((s) => s.create)
  const execute           = useGoalStore((s) => s.execute)
  const select            = useGoalStore((s) => s.select)
  const selectedSandboxId = useSandboxStore((s) => s.selectedId)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!description.trim() || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const goal = await create({
        description: description.trim(),
        packages: [],
        sandbox_ids: selectedSandboxId !== null ? [selectedSandboxId] : [],
      })
      select(goal.id)
      await execute(goal.id)
      onClose()
    } catch {
      setError('任务创建失败，请重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="border-b border-[var(--border)] bg-[var(--bg-raised)] p-3">
      <div className="mb-2 text-[11px] font-medium text-[var(--text-secondary)]">新建任务</div>
      <form onSubmit={(e) => void handleSubmit(e)}>
        <textarea
          className="mb-2 w-full resize-none rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--blue)] focus:outline-none"
          rows={3}
          placeholder="描述你想完成的任务…"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          disabled={submitting}
          autoFocus
        />
        {error !== null && (
          <div className="mb-2 text-[11px] text-red-400">{error}</div>
        )}
        <div className="flex gap-2">
          <Button
            size="sm"
            type="submit"
            disabled={submitting || !description.trim()}
            className="flex-1"
          >
            {submitting ? '创建中…' : '开始执行'}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            type="button"
            disabled={submitting}
            onClick={onClose}
          >
            取消
          </Button>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/UserGoalInput.test.tsx
```

Expected: 2 tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/user/UserGoalInput.tsx web/src/lib/__tests__/UserGoalInput.test.tsx
git commit -m "feat(user-ui): add UserGoalInput — create then execute on submit"
```

---

## Task 5: UserExecutionView component

**Files:**
- Create: `web/src/components/user/UserExecutionView.tsx`
- Create: `web/src/lib/__tests__/UserExecutionView.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
// web/src/lib/__tests__/UserExecutionView.test.tsx
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
    // seq=1 appears before seq=2
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/UserExecutionView.test.tsx
```

Expected: FAIL — `Cannot find module '@/components/user/UserExecutionView'`

- [ ] **Step 3: Write the implementation**

```tsx
// web/src/components/user/UserExecutionView.tsx
import { useGoalStore } from '@/store/goalStore'
import { formatStepLabel } from '@/lib/stepFormatter'
import { UserApprovalCard } from '@/components/user/UserApprovalCard'
import type { GoalStepStatus } from '@/types/goal'

const STEP_ICON: Record<GoalStepStatus, string> = {
  pending:        '○',
  running:        '◌',
  passed:         '✓',
  failed:         '✗',
  awaiting_review:'⏸',
  skipped:        '—',
  rolled_back:    '↩',
}

const STEP_COLOR: Record<GoalStepStatus, string> = {
  pending:        'text-[var(--text-muted)]',
  running:        'text-blue-400',
  passed:         'text-[var(--green,#4ade80)]',
  failed:         'text-red-400',
  awaiting_review:'text-orange-400',
  skipped:        'text-[var(--text-muted)]',
  rolled_back:    'text-[var(--text-muted)]',
}

export function UserExecutionView() {
  const goals      = useGoalStore((s) => s.goals)
  const selectedId = useGoalStore((s) => s.selectedId)
  const goal       = goals.find((g) => g.id === selectedId) ?? null

  if (goal === null) {
    return (
      <div className="flex h-full items-center justify-center text-[13px] text-[var(--text-muted)]">
        选择一个任务或新建任务开始执行
      </div>
    )
  }

  const pendingReview  = goal.reviews?.find((r) => r.status === 'pending') ?? null
  const sortedSteps    = [...(goal.steps ?? [])].sort((a, b) => a.seq - b.seq)

  return (
    <div className="flex h-full flex-col gap-3 overflow-y-auto p-4">
      <div className="text-[13px] font-medium text-[var(--text-secondary)]">
        {goal.description}
      </div>

      {sortedSteps.length === 0 && (goal.status === 'executing' || goal.status === 'created') && (
        <div className="text-[12px] text-[var(--text-muted)]">正在规划…</div>
      )}

      <div className="flex flex-col gap-2">
        {sortedSteps.map((step) => (
          <div
            key={step.id}
            className="flex items-center gap-3 rounded-md border border-[var(--border)] bg-[var(--bg-raised)] px-3 py-2"
          >
            <span className={`w-4 flex-shrink-0 text-center font-mono text-[13px] ${STEP_COLOR[step.status]}`}>
              {STEP_ICON[step.status]}
            </span>
            <span className="text-[12px] text-[var(--text-secondary)]">
              {formatStepLabel(step)}
            </span>
          </div>
        ))}
      </div>

      {pendingReview !== null && (
        <UserApprovalCard goal={goal} review={pendingReview} />
      )}

      {goal.status === 'completed' && (
        <div className="rounded-md border border-[var(--green,#4ade80)] bg-[rgba(74,222,128,0.08)] px-3 py-2 text-[12px] text-[var(--green,#4ade80)]">
          任务已完成
        </div>
      )}

      {goal.status === 'failed' && (
        <div className="rounded-md border border-red-500 bg-[rgba(239,68,68,0.08)] px-3 py-2 text-[12px] text-red-400">
          执行过程中出现错误
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/UserExecutionView.test.tsx
```

Expected: 7 tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/user/UserExecutionView.tsx web/src/lib/__tests__/UserExecutionView.test.tsx
git commit -m "feat(user-ui): add UserExecutionView — steps from goal.steps, ApprovalCard on paused"
```

---

## Task 6: UserShell layout

**Files:**
- Create: `web/src/components/user/UserShell.tsx`

No separate test — UserShell is a thin layout wrapper; behaviour is covered by UserApp test in Task 7.

- [ ] **Step 1: Write the implementation**

```tsx
// web/src/components/user/UserShell.tsx
import { useState } from 'react'
import { UserGoalList } from '@/components/user/UserGoalList'
import { UserGoalInput } from '@/components/user/UserGoalInput'
import { UserExecutionView } from '@/components/user/UserExecutionView'
import { useUIStore } from '@/store/uiStore'

function connectionDot(status: 'checking' | 'online' | 'offline'): string {
  if (status === 'online')  return 'bg-[var(--green,#4ade80)]'
  if (status === 'offline') return 'bg-red-500'
  return 'bg-[var(--text-muted)] animate-pulse'
}

function connectionLabel(status: 'checking' | 'online' | 'offline'): string {
  if (status === 'online')  return '已连接'
  if (status === 'offline') return '连接断开'
  return '连接中…'
}

export function UserShell() {
  const gatewayStatus = useUIStore((s) => s.gatewayStatus)
  const [showInput, setShowInput] = useState(false)

  return (
    <div className="flex min-h-screen flex-col bg-[var(--bg-base,#0a0a0a)]">
      {/* Topbar */}
      <header className="flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg-surface,#111)] px-5 py-3">
        <div className="flex items-center gap-3">
          <span className="text-[14px] font-semibold text-[var(--text-primary)]">PrimitiveBox</span>
          <span className="text-[11px] text-[var(--text-muted)]">AI 任务助手</span>
        </div>
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${connectionDot(gatewayStatus)}`} />
          <span className="text-[11px] text-[var(--text-muted)]">{connectionLabel(gatewayStatus)}</span>
        </div>
      </header>

      {/* Body */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left panel */}
        <div className="flex w-[260px] flex-shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-subtle,#0d0d0d)]">
          {showInput && <UserGoalInput onClose={() => setShowInput(false)} />}
          <div className="min-h-0 flex-1 overflow-hidden">
            <UserGoalList onNewGoal={() => setShowInput(true)} />
          </div>
        </div>

        {/* Right panel */}
        <div className="flex flex-1 flex-col overflow-hidden bg-[var(--bg-surface,#111)]">
          <UserExecutionView />
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/components/user/UserShell.tsx
git commit -m "feat(user-ui): add UserShell layout"
```

---

## Task 7: UserApp root component

**Files:**
- Create: `web/src/UserApp.tsx`
- Create: `web/src/lib/__tests__/UserApp.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
// web/src/lib/__tests__/UserApp.test.tsx
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { UserApp } from '@/UserApp'
import { useSandboxStore } from '@/store/sandboxStore'
import { useUIStore } from '@/store/uiStore'
import { useGoalStore } from '@/store/goalStore'
import { listSandboxes } from '@/api/sandboxes'

// Mock the API modules that UserApp calls directly
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
      selectedId: 'sb-stop',  // load() default — wrong one
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd web && npx vitest run src/lib/__tests__/UserApp.test.tsx
```

Expected: FAIL — `Cannot find module '@/UserApp'`

- [ ] **Step 3: Write the implementation**

```tsx
// web/src/UserApp.tsx
import { useEffect, useState } from 'react'
import { getHealth } from '@/api/client'
import { UserShell } from '@/components/user/UserShell'
import { useGoalStore } from '@/store/goalStore'
import { useSandboxStore } from '@/store/sandboxStore'
import { useUIStore } from '@/store/uiStore'
import { useGoalEventStream } from '@/hooks/useGoalEventStream'

export function UserApp() {
  useGoalEventStream()

  const loadGoals      = useGoalStore((s) => s.load)
  const loadSandboxes  = useSandboxStore((s) => s.load)
  const sandboxes      = useSandboxStore((s) => s.sandboxes)
  const selectSandbox  = useSandboxStore((s) => s.select)
  const setGatewayStatus = useUIStore((s) => s.setGatewayStatus)

  const [sandboxesLoaded, setSandboxesLoaded] = useState(false)

  useEffect(() => {
    void loadGoals()
  }, [loadGoals])

  useEffect(() => {
    void loadSandboxes().then(() => setSandboxesLoaded(true))
  }, [loadSandboxes])

  // After sandboxes load, override selectedId to the first *running* sandbox.
  // sandboxStore.load() sets selectedId to the first sandbox unconditionally,
  // which may not be running.
  useEffect(() => {
    if (!sandboxesLoaded) return
    const running = sandboxes.find((s) => s.status === 'running')
    if (running !== undefined) {
      selectSandbox(running.id)
    }
  }, [sandboxesLoaded, sandboxes, selectSandbox])

  // Initialize gateway status — same pattern as App.tsx.
  // Without this, gatewayStatus stays 'checking' indefinitely at /app.
  useEffect(() => {
    let active = true
    setGatewayStatus('checking')
    void getHealth()
      .then(() => { if (active) setGatewayStatus('online') })
      .catch(() => { if (active) setGatewayStatus('offline') })
    return () => { active = false }
  }, [setGatewayStatus])

  if (sandboxesLoaded && !sandboxes.some((s) => s.status === 'running')) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg-base,#0a0a0a)]">
        <div className="text-center">
          <div className="text-[14px] font-medium text-[var(--text-primary)]">服务未就绪</div>
          <div className="mt-1 text-[12px] text-[var(--text-muted)]">请稍后重试</div>
        </div>
      </div>
    )
  }

  return <UserShell />
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd web && npx vitest run src/lib/__tests__/UserApp.test.tsx
```

Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/UserApp.tsx web/src/lib/__tests__/UserApp.test.tsx
git commit -m "feat(user-ui): add UserApp root — health check, sandbox auto-select, service unavailable guard"
```

---

## Task 8: Wire /app route in main.tsx

**Files:**
- Modify: `web/src/main.tsx`

No separate test — the routing logic is one expression; UserApp mount is already tested in Task 7.

- [ ] **Step 1: Update main.tsx**

Replace the entire file:

```tsx
// web/src/main.tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from '@/App'
import { UserApp } from '@/UserApp'
import '@/index.css'

const isUserRoute = window.location.pathname.startsWith('/app')

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    {isUserRoute ? <UserApp /> : <App />}
  </React.StrictMode>
)
```

- [ ] **Step 2: Run the full test suite to confirm nothing regressed**

```bash
cd web && npx vitest run
```

Expected: all existing tests pass plus all new tests.

- [ ] **Step 3: Manual smoke test**

```bash
# In one terminal:
make run

# In another terminal, open the developer UI:
open http://localhost:5173/

# Open the user UI:
open http://localhost:5173/app
```

Verify:
- `/` loads the existing developer debug UI (Sidebar shows "Developer Debug UI")
- `/app` loads the new user UI with "PrimitiveBox / AI 任务助手" topbar
- Creating a sandbox in `/` and then visiting `/app` auto-selects it
- Creating a goal in `/app` and watching it execute shows steps in the right panel
- When a `paused` goal has a pending review, the ApprovalCard appears
- Clicking 批准 resumes execution; clicking 拒绝 stops it

- [ ] **Step 4: Commit**

```bash
git add web/src/main.tsx
git commit -m "feat(user-ui): wire /app pathname to UserApp in main.tsx"
```
