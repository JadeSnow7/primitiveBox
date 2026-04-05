# User Frontend Design Spec

**Date:** 2026-04-05  
**Status:** Approved (revised after code review)  
**Scope:** `/app` route — end-user facing frontend for PrimitiveBox

---

## 1. Context

The existing `web/` frontend is explicitly a **Developer Debug UI (v0)**. It exposes sandbox IDs, driver types, TTL values, primitive catalogs, raw JSON traces, and execution event kinds (`execution.call`, `execution.result`, etc.) — all unsuitable for non-technical end users.

This spec defines a separate user-facing view at `/app` for **end users**: people who describe a task in natural language, watch it execute step by step, and approve or reject AI actions before they are committed.

---

## 2. Goals

- Let a non-technical user create a goal, watch progress in plain language, and approve critical actions.
- Maintain a list of past and active goals with status badges.
- Zero exposure of internal runtime concepts (sandbox, driver, TTL, primitive kind).
- No new backend APIs — reuse `goalStore`, `sandboxStore`, `uiStore`, and `useGoalEventStream`.

---

## 3. Non-Goals

- Role-based auth or login (out of scope for v1).
- Mobile-optimized layout (desktop-first).
- Real-time collaboration (one user, one session).
- Modifying any existing developer UI behavior.

---

## 4. Interaction Model

**Goal → Progress → Approval → Resume → Result**

1. User types a goal in natural language and submits.
2. `UserGoalInput` calls `goalStore.create(...)` then immediately `goalStore.execute(goal.id)`.
3. `useGoalEventStream` subscribes to SSE; on each event, calls `goalStore.refresh(goalId)` to update the persistent `Goal` model.
4. `UserExecutionView` reads `goal.steps` and `goal.reviews` (from `goalStore`) — not `timelineStore`.
5. When `goal.status === 'paused'` and a `GoalReview` with `status === 'pending'` exists, `UserApprovalCard` appears.
6. User clicks Approve → `goalStore.approve(goalId, reviewId)` then `goalStore.resume(goalId)`.  
   User clicks Reject → `goalStore.reject(goalId, reviewId, reason)`.
7. On completion, `goal.status === 'completed'`; final step outputs are shown inline.

> **Why not `timelineStore` / `orchestratorStore`?**  
> `timelineStore` is in-memory only (max 100 entries, keyed by `groupId` not `goal_id`) and is cleared on refresh.  
> `orchestratorStore` is the in-memory agent-loop gate used by the developer Workspace; it has no persistent approval flow.  
> The user UI must use `Goal.steps / reviews / verifications` as the single source of truth so that switching goals or refreshing the page never loses state.

---

## 5. Entry Point

**No new routing library required.**

`main.tsx` checks `window.location.pathname.startsWith('/app')`:
- `true` → renders `<UserApp />`
- `false` → renders existing `<App />` (unchanged)

The Vite dev server and production build serve the same `index.html`; the path check happens client-side at runtime.

---

## 6. Layout

```
┌──────────────────────────────────────────────────────┐
│  Topbar: "PrimitiveBox" + "AI 任务助手" + 连接状态     │
├─────────────────┬────────────────────────────────────┤
│  Goal List      │  Execution View                    │
│  (260px fixed)  │  (flex-1)                          │
│                 │                                    │
│  [+ 新建任务]   │  Goal title                        │
│  ─────────────  │                                    │
│  Goal 1  ⏸     │  ✓ Step 1 (passed)                 │
│  Goal 2  ✓     │  ✓ Step 2 (passed)                 │
│  Goal 3  ▶     │  ○ Step 3 (pending)                │
│                 │                                    │
│                 │  ┌─ Approval Card ──────────────┐  │
│                 │  │ ⏸ AI 需要你确认后继续         │  │
│                 │  │ 即将执行：写入 report.pdf      │  │
│                 │  │ [批准]  [拒绝]                │  │
│                 │  └──────────────────────────────┘  │
└─────────────────┴────────────────────────────────────┘
```

---

## 7. New Files

All new files live under `web/src/components/user/` and `web/src/`. The only existing file modified is `main.tsx` (the path check, ~5 lines).

| File | Responsibility |
|------|----------------|
| `web/src/UserApp.tsx` | Root component. Calls `getHealth()`, loads goals and sandboxes, auto-selects sandbox, renders `UserShell`. |
| `web/src/components/user/UserShell.tsx` | Layout: Topbar + two-panel body. |
| `web/src/components/user/UserGoalList.tsx` | Left panel. Lists goals with status badges (from `goal.status`). "新建任务" button. |
| `web/src/components/user/UserGoalInput.tsx` | New-goal input: textarea + submit. On submit: `create(...)` then `execute(goal.id)`. |
| `web/src/components/user/UserExecutionView.tsx` | Right panel. Reads `goal.steps` and `goal.reviews` from `goalStore`. Shows ApprovalCard when `goal.status === 'paused'` and a pending review exists. |
| `web/src/components/user/UserApprovalCard.tsx` | Approval UI. Reads `GoalReview` fields; calls `approve + resume` or `reject`. |
| `web/src/lib/stepFormatter.ts` | Pure function: maps `GoalStep` to a human-readable Chinese label. |

---

## 8. Reused (Unchanged)

| Item | How it is used |
|------|----------------|
| `useGoalStore` | Create, execute, list, select, refresh goals; approve/reject/resume reviews |
| `useSandboxStore` | Load sandboxes; explicitly select first `running` sandbox |
| `useUIStore` | Read/write `gatewayStatus` for topbar connection indicator |
| `useGoalEventStream` hook | SSE subscription — triggers `goalStore.refresh(goalId)` on each event |
| `getHealth()` from `@/api/client` | Called in `UserApp.tsx` on mount to initialize `gatewayStatus` |
| `orchestratorStore` | **Not used** by user UI |
| `timelineStore` | **Not used** by user UI |

---

## 9. stepFormatter.ts

Maps a `GoalStep` to a plain-language Chinese string shown in the step list.

Input: `GoalStep` (fields: `primitive`, `input`, `status`)

| `GoalStep.status` | Display |
|---|---|
| `pending` | ○ (gray dot, label from primitive map) |
| `running` | ◌ (animated blue dot) |
| `passed` | ✓ (green) |
| `failed` | ✗ (red) |
| `awaiting_review` | ⏸ (orange — ApprovalCard takes over) |
| `skipped` | — 已跳过 |
| `rolled_back` | ↩ 已回滚 |

`primitive` verb map (extensible):

| primitive | label prefix |
|---|---|
| `fs.read` | 读取文件 |
| `fs.write` | 写入文件 |
| `shell.exec` | 执行命令 |
| `http.fetch` | 请求网络 |
| *(unknown)* | `primitive` value as-is |

Key input param (e.g. `path`, `url`, `command`) appended after the label prefix when present.

---

## 10. ApprovalCard Content Source

`UserApprovalCard` reads from a `GoalReview` object (from `goal.reviews`), using:

- `review.side_effect` (string) — shown as the action description ("即将执行：…")
- `review.reversible` (boolean) — "此操作不可撤销" warning when `false`
- `review.risk_level` (string) — border color: `low` = gray, `medium` = orange, `high` = red

**Approval flow:**
```
User clicks 批准
  → goalStore.approve(goalId, review.id)
  → goalStore.resume(goalId)        ← required; approval alone does not continue execution
  → goalStore.refresh(goalId)       ← already done inside approve() and resume()

User clicks 拒绝
  → goalStore.reject(goalId, review.id, reason?)
  → goalStore.refresh(goalId)       ← already done inside reject()
```

---

## 11. Goal Status Mapping (Left Panel Badges)

Source: `goal.status` (`GoalStatus`) — persistent, survives page refresh.

| `goal.status` | Badge color | Label |
|---|---|---|
| `created` | Gray | 未开始 |
| `executing` / `verifying` | Blue, animated | 执行中 |
| `paused` | Orange | 待确认 |
| `completed` | Green | 已完成 |
| `failed` | Red | 失败 |

---

## 12. Sandbox Auto-Selection

`UserApp.tsx` on mount:
1. Call `sandboxStore.load()`. *(This sets `selectedId` to the first sandbox in the list, regardless of status.)*
2. After load, read `sandboxStore.sandboxes` and find the first with `status === 'running'`.
3. If found, call `sandboxStore.select(id)` to override whatever `load()` set.
4. If none found → show a full-screen "服务未就绪，请稍后重试" message. Do not expose sandbox creation UI.

> `load()` defaults `selectedId` to the first sandbox unconditionally. Step 3 explicitly overrides this with a running sandbox.

---

## 13. Gateway Status Initialization

`UserApp.tsx` must call `getHealth()` itself on mount, identical to how `App.tsx` does it:

```ts
useEffect(() => {
  let active = true
  setGatewayStatus('checking')
  void getHealth()
    .then(() => { if (active) setGatewayStatus('online') })
    .catch(() => { if (active) setGatewayStatus('offline') })
  return () => { active = false }
}, [setGatewayStatus])
```

Without this, `gatewayStatus` stays `'checking'` indefinitely when navigating directly to `/app`.

---

## 14. Error Handling

| Scenario | User-visible message |
|---|---|
| Gateway offline | "无法连接服务，请检查网络" |
| Goal creation fails | "任务创建失败，请重试" |
| Execute call fails | "任务启动失败，请重试" |
| Execution error (`goal.status === 'failed'`) | "执行过程中出现错误" (no raw message) |
| No running sandbox | "服务未就绪，请稍后重试" |

No raw error objects, stack traces, primitive kind names, or sandbox IDs are shown to the user.

---

## 15. Testing

| Test | Coverage |
|------|----------|
| `stepFormatter.test.ts` | All `GoalStep.status` values and all known primitives map correctly; unknown primitive falls back gracefully |
| `UserApprovalCard.test.tsx` | Renders `side_effect` text; "不可撤销" shown when `reversible=false`; Approve calls `approve` then `resume`; Reject calls `reject` |
| `UserGoalList.test.tsx` | All `GoalStatus` values render correct badge color and label; clicking a goal calls `select(id)` |
| `UserExecutionView.test.tsx` | Renders steps from `goal.steps`; ApprovalCard shown when `goal.status === 'paused'` and pending review exists; no ApprovalCard when no pending review |
| `UserApp.test.tsx` | Calls `getHealth()` on mount; selects first `running` sandbox (not just first sandbox); shows "服务未就绪" when no running sandbox |
| `UserGoalInput.test.tsx` | Submit calls `create(...)` then `execute(goal.id)` in order; shows error if either fails |

---

## 16. Out of Scope for v1

- URL-based deep linking to individual goals (e.g. `/app/goals/:id`)
- File upload / drag-and-drop input
- Dark/light theme toggle
- Internationalization beyond simplified Chinese labels
- Any modification to the developer UI at `/`
