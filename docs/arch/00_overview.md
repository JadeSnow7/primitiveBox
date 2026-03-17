# PrimitiveBox 架构概览

> 本文描述 M0～M3 收口后的实际架构状态。
> 设计意图与当前实现的差异在文末单独标注。

---

## 定位

Checkpointed sandbox runtime for AI agents。

基于原语和结构化 CVR（Checkpoint-Verify-Recover）语义，
为 AI agent 提供可观测、可恢复、可验证的执行环境。

---

## 核心原则（不变量）

1. 原语是显式契约，不是松散的 prompt 约定
2. 副作用在执行前可 checkpoint（Layer A）
3. 成功由验证决定，不由模型自己声明（Layer B）
4. 失败可恢复，不是终止（`RecoveryDecisionTree`）
5. 执行历史可重放、可检查（`ExecutionTrace` / `StepRecord`）

---

## 执行层次

```
Client
  → Host Gateway（认证 / 路由 / SQLite 状态 / SSE 事件流）
      → CVR Core（CheckpointManifest / VerifyStrategy / RecoveryDecisionTree）
          → Sandbox pb server（Primitive Registry / 执行引擎 / ExecutionTrace）
              → 系统原语 / 代码原语 / App 原语（通过注册协议接入）
```

关键路由边界：
- `POST /rpc`：host-workspace 模式，使用 host-local primitive registry
- `POST /sandboxes/{id}/rpc`：代理到 sandbox pb server，执行在 sandbox 内
- `POST /sandboxes/{id}/rpc/stream`：SSE 流式代理，同上

---

## 当前实现状态

### 系统原语（`internal/primitive/`）

| 原语前缀 | 文件 | 说明 |
|----------|------|------|
| `fs.*` | `fs.go` | 文件读写，`.go` 文件自动 astdiff |
| `shell.*` | `shell.go` | 受控 shell 执行 |
| `state.*` | `state.go` | git-backed checkpoint / restore |
| `verify.*` | `verify.go`, `verify_command.go` | 验证命令执行 |
| `macro.*` | `macro.go` | 复合原语（checkpoint → write → verify） |
| `code.*` | `code.go` | 代码搜索、符号提取 |
| `browser.*` | `browser.go` | 浏览器自动化 |
| `db.*` | `db.go` | 只读数据库访问 |
| `workspace.*` | `workspace.go` | 工作区元数据 |

### CVR Core（`internal/cvr/`）

**`CheckpointManifest`**（`manifest.go`）

结构化快照元数据，17 个字段。关键字段：
- `CheckpointID`：关联 `state.checkpoint` 返回的 git commit hash
- `PrevCheckpointID`：链表结构，支持 `GetManifestChain`
- `Intent`：`PrimitiveIntent`（category / reversible / risk_level / affected_scopes）
- `Corrupted` + `CorruptReason`：损坏标记，由 `MarkCorrupted` 写入

**`CVRCoordinator.Execute`**（`coordinator.go`）

执行顺序：

```
1. shouldCheckpoint(intent)?
   → true:  调用 state.checkpoint，失败返回 LayerAErr（短路，禁止继续执行）
   → false: LayerAOutcome = "skipped"
2. req.Exec.Execute(primitiveID, params)        ← 原语执行
3. VerifyStrategy.Run(exec, execResult, manifest) ← Layer B 验证（可选）
4. DecisionTree.Decide(RecoveryCtx)              ← 决策恢复动作
```

checkpoint 触发策略：
- `IntentMutation` + `Reversible=false` → 强制 checkpoint
- `IntentMutation` + `Reversible=true` + `RiskHigh` → 强制 checkpoint
- 其余（query / verification / rollback，或 low/medium risk）→ 跳过

**`RecoveryDecisionTree`**（`recovery_tree.go`）

五节点有序链，第一个 `Match` 胜出：

| 节点 | 条件 | 动作 |
|------|------|------|
| `IrreversibleMutationNode` | `!Reversible` && `VerifyOutcomeFailed` | `rollback` |
| `MaxAttemptsNode` | `attempt >= maxRetries` | `escalate` |
| `DuplicateNode` | `FailureKindDuplicate` | `abort` |
| `TimeoutNode` | `FailureKindTimeout` | `retry` |
| `DefaultRetryNode` | （兜底） | `retry` |

`MaxCVRDepth = 5`，超限返回 `ErrCVRDepthExceeded`，不执行任何操作。

**`TestSuiteStrategy`**（`strategy_test_suite.go`）

`VerifyStrategy` 的唯一实现（当前）。运行测试命令，解析 pass/fail。

### 代码原语语义增强（`internal/primitive/astdiff/`）

`astdiff.Diff(before, after []byte) ([]SymbolChange, error)`

分析 Go 源文件顶层声明变化，返回 `SymbolChange` 列表：
- `func_signature`：函数签名变更
- `type_added` / `type_removed`：类型增删
- `method_added`：方法新增
- `field_changed`：结构体字段变更

`fs.write` 自动触发，结果写入 `result.Data["symbol_changes"]`，
`intent.AffectedScopes` 去重后追加变更的符号名。
astdiff 失败记录 warning，不中断写入。

### Application Primitive 注册协议（`internal/primitive/app_manifest.go`）

注册流程：

```
Sandbox app process
  → AppServer.primitive(name, socket_path, ...)  ← Python SDK 装饰器
  → POST /rpc { method: "app.register", ... }    ← X-PB-Origin: sandbox
  → isSandboxRequest check（manager == nil 的 pb server）
  → inMemoryAppRegistry.Register(manifest)
```

调用流程：

```
Client → Router.Route(method, params)
  → registry.Get(method) 未命中
  → appRegistry.Get(method) 命中
  → net.Dial("unix", manifest.SocketPath)
  → JSON-RPC over newline-delimited JSON
```

Python `AppServer`（`sdk/python/primitivebox/app.py`）
`HTMLLayoutServer` 参考实现（`sdk/python/primitivebox/html_layout.py`）：

| 原语 | 类别 | Reversible |
|------|------|-----------|
| `style.read` | query | true |
| `style.apply` | mutation | true |
| `style.apply_tokens` | mutation | true |
| `verify.contrast` | verification | true |
| `verify.structure` | verification | true |

### 控制平面（`internal/control/sqlite_store.go`）

SQLite 表：

| 表 | 用途 |
|----|------|
| `sandboxes` | 沙箱状态（driver / config / status / rpc_endpoint / TTL） |
| `events` | 事件日志（idx_events_sandbox_id / idx_events_type） |
| `trace_steps` | CVR 执行追踪（IntentSnapshot / LayerAOutcome / StrategyOutcome 等） |
| `checkpoint_manifests` | checkpoint 语义元数据（idx_cp_sandbox / idx_cp_trace） |

### 开发者调试界面（`cmd/pb-ui/`）

React 18 + TypeScript + Vite，最小依赖（仅 react / react-dom）。

单文件架构（`src/App.tsx`，≈270 行）：
- 状态管理：`useState` / `useMemo`（无外部 store 库）
- SSE 订阅：`new EventSource("/api/v1/events/stream")`，事件最多缓存 200 条
- 沙箱列表：`GET /api/v1/sandboxes`
- 暗色主题：CSS 变量（`--bg` / `--panel` / `--text` / `--ok` / `--warn` / `--danger`）
- API 调用为同源请求（开发时 Vite 无 proxy，通过 Go embed 同端口提供）

---

## 关键包依赖约束

禁止的依赖方向（会造成循环）：
- `internal/cvr` → `internal/runtrace`
- `internal/runtrace` → `internal/cvr`
- `internal/primitive/astdiff` → `internal/cvr` 以外的 internal 包

`IntentContextKey` 唯一定义：`internal/runtime/runtime.go`
（`= runtimectx.IntentContextKey`，两者指向同一底层 key 值）

CVRResult 与 ExecutionTrace 的解耦：
- `CVRResult.TraceRef = string`（traceID 引用）
- `StepRecord.IntentSnapshot = string`（JSON 序列化的 `PrimitiveIntent`）
- `engine.go` 负责关联，两个包不互相依赖

---

## 设计意图与当前实现的差异

| 设计意图 | 当前实现 | 备注 |
|----------|----------|------|
| App 原语持久化注册 | 内存注册，重启丢失 | Later 项 |
| Kubernetes runtime | `kubernetes.go` 有骨架，测试未稳定 | Later 项 |
| `AIJudgeStrategy` | 接口已预留，未实现 | `CVRDepth` 防护已就绪 |
| human-in-the-loop escalate | `RecoveryAction.escalate` 枚举已有 | 前端确认弹窗待实现（N2/N3） |
| `engine.go` 执行 rollback | CVR 决策树返回 `rollback`，但 engine 映射为 `ActionPause` | 实际 rollback 未执行，下阶段修复 |
| `CheckpointManifest.SandboxID` | `CVRCoordinator` 不持有 sandbox 上下文，字段为空 | 需由调用方在 `CVRRequest` 中传入 |
