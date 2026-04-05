# User Frontend Design Spec

**Date:** 2026-04-05  
**Status:** Approved  
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
- No new backend APIs or store logic — reuse everything already in place.

---

## 3. Non-Goals

- Role-based auth or login (out of scope for v1).
- Mobile-optimized layout (desktop-first).
- Real-time collaboration (one user, one session).
- Modifying any existing developer UI behavior.

---

## 4. Interaction Model

**Goal → Progress → Approval → Result**

1. User types a goal in natural language and submits.
2. AI executes, emitting steps visible as a human-readable progress list.
3. Before irreversible or sensitive actions, execution pauses with an **Approval Card**.
4. User clicks Approve or Reject.
5. Execution resumes or terminates. Final result is shown inline.

This maps directly to the existing `AWAITING_REVIEW` / `pending_review` orchestrator flow.

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
│  Goal 1  ⏸     │  ✓ Step 1 (completed)              │
│  Goal 2  ✓     │  ✓ Step 2 (completed)              │
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

All new files live under `web/src/components/user/` and `web/src/`. No existing files are modified except `main.tsx` (the path check, ~5 lines).

| File | Responsibility |
|------|----------------|
| `web/src/UserApp.tsx` | Root component. Initializes stores, auto-selects sandbox, renders `UserShell`. |
| `web/src/components/user/UserShell.tsx` | Layout: Topbar + two-panel body. |
| `web/src/components/user/UserGoalList.tsx` | Left panel. Lists goals with status badges. "新建任务" button. |
| `web/src/components/user/UserGoalInput.tsx` | New-goal input: textarea + submit button. Shown inline above the list or as a modal. |
| `web/src/components/user/UserExecutionView.tsx` | Right panel. Renders step list for selected goal; shows ApprovalCard when needed. |
| `web/src/components/user/UserApprovalCard.tsx` | Orange-bordered card with action description, Approve and Reject buttons. |
| `web/src/lib/stepFormatter.ts` | Pure function: maps `TimelineEntry` to a human-readable Chinese label. |

---

## 8. Reused (Unchanged)

| Item | How it is used |
|------|----------------|
| `useGoalStore` | Create goals, list goals, select active goal |
| `useOrchestratorStore` | Read phase, call `approve()` / `reject()` |
| `useTimelineStore` | Read `entries` for the selected goal |
| `useSandboxStore` | Auto-select first running sandbox; hidden from user |
| `useGoalEventStream` hook | SSE subscription for real-time updates |
| All existing backend APIs | Zero changes |

---

## 9. stepFormatter.ts

Maps `TimelineEntry['kind']` and metadata to a plain-language string shown in the step list.

| kind | Display label |
|------|---------------|
| `plan` | 正在规划… |
| `execution.call` | Uses `entry.method` to look up a verb map (e.g. `"fs.read"` → `"读取文件"`) + key param (e.g. `path`). Falls back to `entry.method` if no mapping found. |
| `execution.result` | 已完成 (no raw result shown; a future iteration can add a result summary) |
| `execution.pending_review` | Not shown in step list — rendered by `UserApprovalCard` instead |
| `execution.rejected` | 已拒绝 |
| `execution.skipped` | 已跳过 |
| `execution.simulated` | 模拟执行（演示模式） |
| `ui` | Hidden — not shown to the user |

### ApprovalCard content source

`execution.pending_review` carries three fields used directly by `UserApprovalCard`:

- `side_effect: string` — already human-readable; shown as the action description ("即将执行：…")
- `reversible: boolean` — shown as a secondary label ("此操作不可撤销" when `false`)
- `risk_level: 'low' | 'medium' | 'high'` — drives border color (low = gray, medium = orange, high = red)

---

## 10. Goal Status Mapping

| Orchestrator phase | Badge color | Label |
|---|---|---|
| `planning` / `executing` | Blue, animated | 执行中 |
| `AWAITING_REVIEW` | Orange | 待确认 |
| `done` | Green | 已完成 |
| `failed` | Red | 失败 |
| `idle` | Gray | 未开始 |

---

## 11. Sandbox Auto-Selection

`UserApp.tsx` on mount:
1. Call `sandboxStore.load()`.
2. If `selectedId` is already set → use it.
3. Else pick the first sandbox with `status === 'running'` and call `sandboxStore.select(id)`.
4. If none available → show a full-screen "服务未就绪，请稍后重试" message. Do not expose sandbox creation UI.

---

## 12. Topbar Simplification

The user topbar shows only:
- Product name: "PrimitiveBox"
- Subtitle: "AI 任务助手"
- Connection status: green dot "已连接" / red dot "连接断开" (maps to `gatewayStatus` from `useUIStore`)

No sandbox ID, driver, TTL, or "New Sandbox" button.

---

## 13. Error Handling

| Scenario | User-visible message |
|---|---|
| Gateway offline | "无法连接服务，请检查网络" |
| Goal creation fails | "任务创建失败，请重试" |
| Execution error | "执行过程中出现错误：`<error.message>`" |
| No running sandbox | "服务未就绪，请稍后重试" |

No raw error objects, stack traces, or primitive kind names are shown to the user.

---

## 14. Testing

Each new component and utility gets a Vitest unit test:

| Test | Coverage |
|------|----------|
| `stepFormatter.test.ts` | All `kind` values map to expected strings; unknown kind returns fallback |
| `UserApprovalCard.test.tsx` | Renders action description; Approve fires `approve()`; Reject fires `reject()` |
| `UserGoalList.test.tsx` | Renders goals with correct status badges; clicking selects the goal |
| `UserExecutionView.test.tsx` | Shows steps; hides `ui` kind entries; shows ApprovalCard when phase is `AWAITING_REVIEW` |
| `UserApp.test.tsx` | Auto-selects sandbox on mount; shows "服务未就绪" when no sandbox available |

---

## 15. Out of Scope for v1

- URL-based deep linking to individual goals (e.g. `/app/goals/:id`)
- File upload / drag-and-drop input
- Dark/light theme toggle
- Internationalization beyond simplified Chinese labels
- Any modification to the developer UI at `/`
