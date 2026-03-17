# Frontend 开发文档

前端位于 `cmd/pb-ui/`。构建产物由 Go embed 嵌入并由 `cmd/pb/` 启动的 HTTP server 托管。

---

## 快速启动

前提：后端已运行（默认监听 `localhost:8080`，提供 `/api/v1/` 和 SSE 端点）

```bash
cd cmd/pb-ui
npm install
npm run dev      # Vite dev server，http://localhost:5173
npm run build    # 生产构建，输出到 cmd/pb-ui/dist/
```

> **注意**：Vite 未配置 proxy（`vite.config.ts` 无 `server.proxy`）。
> 开发模式下前端直接请求 `localhost:5173` 上的同路径，需要后端开启 CORS。
> 后端 CORS 当前允许 `http://localhost:5173`（`internal/rpc/server.go:154`）。

---

## 目录结构

```
cmd/pb-ui/
  src/
    App.tsx        主组件，包含全部 UI 逻辑（~270 行）
    main.tsx       React 入口，挂载 App
    styles.css     暗色主题 CSS，所有变量定义在此
  dist/            生产构建输出（Git 已追踪，用于 Go embed）
  index.html       HTML 模板
  vite.config.ts   构建配置（无 proxy，outDir=dist）
  package.json     依赖：react 18 + react-dom，无外部 UI 库
```

当前为单文件架构。所有状态（沙箱列表、事件流、选中项）通过 `useState` / `useMemo` 在 `App.tsx` 内管理。

---

## 依赖说明

`package.json` 中只有 `react` / `react-dom`（运行时）和 Vite + TypeScript 类型（开发时）。

**无**以下库：Zustand、Tailwind、shadcn/ui、@rjsf/core、Monaco Editor。

如需添加依赖，先评估是否会导致构建产物体积显著增大（影响 Go embed 大小）。

---

## 设计规范

**CSS 变量**（定义在 `styles.css` `:root`）：

| 变量 | 用途 |
|------|------|
| `--bg` | 页面背景 `#0a1016` |
| `--panel` | 面板背景（半透明深蓝） |
| `--panel-edge` | 面板边框（半透明） |
| `--text` | 主文字 `#edf5ff` |
| `--muted` | 次级文字 `#92a5bc` |
| `--accent` | 强调色 `#79d8ff` |
| `--accent-strong` | 强调色 2 `#ff9f5a` |
| `--ok` | 成功 / running `#59e39b` |
| `--warn` | 警告 / retry `#ffca6a` |
| `--danger` | 失败 / error `#ff7b7b` |

**字体**：`Avenir Next Condensed`（UI 文本），系统字体栈兜底（Helvetica Neue / Arial Narrow）。

---

## SSE 订阅

`App.tsx` 中通过 `useEffect` 直接管理 SSE 连接生命周期：

```tsx
const source = new EventSource("/api/v1/events/stream");
```

当前订阅的事件类型：
`rpc.started` / `rpc.completed` / `rpc.error` /
`sandbox.created` / `sandbox.started` / `sandbox.stopped` / `sandbox.destroyed` / `sandbox.reaped` /
`db.progress` / `browser.progress`

事件写入本地 state：`setEvents(current => [payload, ...current].slice(0, 200))`
（最新在前，最多 200 条）。

组件 unmount 时 `source.close()`（`useEffect` cleanup）。

---

## API 调用

所有后端调用在 `App.tsx` 中直接 `fetch`，路径为同源路径：

| 用途 | 接口 |
|------|------|
| 沙箱列表 | `GET /api/v1/sandboxes` |
| SSE 事件流 | `GET /api/v1/events/stream` |

如需新增后端调用，在 `App.tsx` 中增加对应 `useEffect` 或事件处理函数。
若 `App.tsx` 超过 400 行，考虑拆出独立组件或自定义 hook。

---

## 生产构建与 Go embed

```bash
cd cmd/pb-ui && npm run build
```

构建产物输出到 `cmd/pb-ui/dist/`，已被 Go embed（`cmd/pb/`）引用。
**每次修改前端后需重新 build 才能通过 `make build` 更新二进制。**

`Makefile` 中无自动前端构建步骤，需手动触发。

---

## 修改现有组件

`App.tsx` 中主要区域：

| 区域 | 说明 |
|------|------|
| `App()` state | `sandboxes` / `events` / `selectedSandbox` / `connectionState` |
| 顶部 hero panel | stream 状态、沙箱数量、事件缓冲数 |
| 左侧 aside | Sandbox Fleet 列表，点击切换选中 sandbox |
| 右侧主区域 | 选中 sandbox 详情 + 过滤后的事件列表 |

新增字段展示：在对应 JSX 区域直接访问 state 变量，无需修改 store 或 hook。

---

## 已知限制

- Vite dev 无 proxy：后端必须先启动且开启 CORS，否则 API 请求全部 403/CORS error
- SSE 断线不自动重连：`source.onerror` 只更新状态文字，不重建连接；网络断开需手动刷新
- 无 primitive 调用面板：当前 UI 仅展示事件流和沙箱状态，不支持直接调用原语
- `dist/` 已 commit 到 Git：直接修改 `src/` 后不 build 会导致二进制与源码不一致
- TypeScript 严格模式：`tsconfig.json` 若存在类型错误，`npm run build` 会失败
