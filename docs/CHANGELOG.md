# Changelog

---

## 阶段修复 · Bug Fixes（M0～M3 收口后）

### P1 修复

**CVRCoordinator 伪造 checkpoint ID**
- 问题：`LayerAOutcome` 报告 `checkpoint_created` 但从未调用 `state.checkpoint`，
  `CheckpointID` 为随机生成的 `cp-...` 字符串，rollback 路径指向不存在的快照
- 修复：`Execute` 路径现在通过真实 `state.checkpoint` 接口创建快照，
  失败时返回 `LayerAErr` 并短路，不继续执行原语
- 影响文件：`internal/cvr/coordinator.go`

**sandbox RPC 代理丢弃 X-PB-Origin header**
- 问题：`AppServer.primitive()` 通过 `POST /sandboxes/{id}/rpc` 注册时，
  代理层只透传 `Content-Type`，`X-PB-Origin: sandbox` 被丢弃，
  `app.register` 在 `isSandboxRequest` 检查中始终返回 false
- 修复：`proxySandboxRequest` 显式透传 `X-PB-Origin`（`server.go:573`），其余 header 不变
- 影响文件：`internal/rpc/server.go`

### P2 修复

**app primitive 重复注册静默覆盖**
- 问题：`inMemoryAppRegistry.Register` 直接赋值，
  第二个 app 可以无声劫持已注册原语的路由
- 修复：注册前检查 name 冲突，返回包含 name 和既有 `AppID` 的结构化错误；
  更新需先 `Unregister` 再 `Register`
- 影响文件：`internal/primitive/app_manifest.go`

**cvrExecutorAdapter 未注入 PrimitiveIntent 到 context**
- 问题：`fs.write` 的 astdiff 路径依赖 ctx 中的 `IntentContextKey`，
  CVR 编排路径转发原始 ctx，`affected_scopes` 和 `symbol_changes` 永远不被填充，
  `TestWriteFile_GoFile_SymbolChangesPresent` 失败
- 修复：`cvrExecutorAdapter` 增加 `intent` 字段，`Execute` 前将 intent 注入 ctx
- 影响文件：`internal/orchestrator/engine.go`

### 结构性修复

**IntentContextKey 双重定义**
- 问题：`internal/runtimectx/context.go` 和 `internal/runtime/runtime.go`
  各自持有 `IntentContextKey` 变量，`fs.go` 读取 `runtimectx` 的 key，
  编排路径写入 `runtime` 的 key，两者若不等则行为静默错误
- 修复：`internal/runtime/runtime.go` 中定义 `IntentContextKey = runtimectx.IntentContextKey`，
  确保两个引用指向同一个底层 key 值；`internal/runtimectx/context.go` 保留作为辅助访问器
  （`WithIntent` / `IntentFromContext`），`fs.go` 通过 `runtimectx.IntentFromContext` 读取
- 影响文件：`internal/runtime/runtime.go`，`internal/runtimectx/context.go`，
  `internal/primitive/fs.go`

---

## M3 · Application Primitive 注册协议

**新增**
- `AppPrimitiveManifest` 类型（`internal/primitive/app_manifest.go`）
  含 `AppID` / `Name` / `SocketPath` / `InputSchema` / `OutputSchema` / `Intent`
- `inMemoryAppRegistry`（线程安全，`sync.RWMutex`）
- `AppRouter` 集成：系统 registry 未命中后查询 `AppPrimitiveRegistry`，
  命中后通过 Unix socket JSON-RPC 转发（`internal/sandbox/router.go`）
- `app.register` RPC 端点，通过 `X-PB-Origin: sandbox` 限制为 sandbox 内调用
- Python `AppServer`（`sdk/python/primitivebox/app.py`）
  Unix socket JSON-RPC server，newline-delimited JSON 协议
- `HTMLLayoutServer`（`sdk/python/primitivebox/html_layout.py`）
  五个原语：`style.read` / `style.apply` / `style.apply_tokens` /
  `verify.contrast` / `verify.structure`
  依赖：`beautifulsoup4` / `tinycss2`，WCAG 2.1 对比度本地计算

**测试**
- `TestAppRouter_AppPrimitive_NotFound` / `TestAppRegister_RejectOnHostGateway`
  （`internal/rpc/app_register_test.go`，`internal/sandbox/router_app_test.go`）
- Python SDK：`test_primitive_registration` / `test_dispatch_via_socket` /
  `test_dispatch_method_not_found`
- `sdk/python/tests/test_html_layout.py`（覆盖全部五个原语）

---

## M2 · 代码原语 AI 化

**新增**
- `internal/primitive/astdiff/astdiff.go`：AST 级语义 diff
  仅分析顶层声明，返回 `SymbolChange` 列表（`func_signature` /
  `type_added` / `type_removed` / `method_added` / `field_changed`）
- `fs.write` 写入 `.go` 文件后自动调用 `astdiff.Diff`，
  `symbol_changes` 写入 `Result.Data`，`AffectedScopes` 去重后写入 intent
- `TestSuiteStrategy`（`internal/cvr/strategy_test_suite.go`）
  `passed=false` + `Reversible=true` → rollback；
  `passed=false` + `Reversible=false` → escalate

**关键约束**
- `astdiff` 失败时记录 warning，不中断执行（增强信息，非强依赖）
- `IntentContextKey` 通过 `context.WithValue` 传递，唯一定义在 `internal/runtime/runtime.go`

---

## M1 · CVR 核心闭环

**新增**
- `CVRCoordinator`（`internal/cvr/coordinator.go`）
  完整的 checkpoint → execute → verify → recover 流程；
  `CVRDepth` 超限返回 `ErrCVRDepthExceeded`，不执行任何操作
- `RecoveryDecisionTree`（`internal/cvr/recovery_tree.go`）
  五节点有序链：`IrreversibleMutationNode` → `MaxAttemptsNode` → `DuplicateNode` →
  `TimeoutNode` → `DefaultRetryNode`；无匹配默认 `abort`
- `ExecutionTrace` 扩展（`internal/runtrace/runtrace.go`）
  新增 CVR 字段：`IntentSnapshot` / `LayerAOutcome` / `StrategyName` /
  `StrategyOutcome` / `RecoveryPath` / `CVRDepthExceeded` / `AffectedScopes`
- SQLite `checkpoint_manifests` 表（`internal/control/sqlite_store.go`），
  `idx_cp_sandbox` / `idx_cp_trace` 索引
- `CheckpointManifestStore` 四个方法实现：
  `SaveManifest` / `GetManifest` / `GetManifestChain` / `MarkCorrupted`

**关键设计决策**
- `CVRResult` 不持有 `ExecutionTrace` 对象（避免 `cvr→runtrace` 循环依赖），
  改为 `TraceRef string`，由 `engine.go` 负责关联
- `IntentSnapshot` 在 `StepRecord` 中存为 JSON string，
  同样避免 `runtrace→cvr` 的包依赖

---

## M0 · CVR 基础类型

**新增**（`internal/cvr/manifest.go`）
- `LayerAErr` 包装类型，携带短路语义（`.Unwrap()` 支持 `errors.Is`）
- `MaxCVRDepth = 5`，`ErrCVRDepthExceeded`
- `IntentCategory` 枚举：`mutation` / `query` / `verification` / `rollback`
- `RiskLevel` 枚举：`low` / `medium` / `high`
- `CheckpointTrigger` 枚举：`manual` / `intent_policy` / `strategy_forced`
- `CheckpointReason` 枚举（6 个值）
- `CallFrame` / `EffectEntry` / `AppStateSnapshot`
- `CheckpointManifest`（17 个字段，含链表前驱 `PrevCheckpointID`、
  `Corrupted` 标志、`FilesModifiedSincePrev`）
- `CheckpointManifestStore` 接口（4 个方法）
- `VerifyOutcome` 枚举：`passed` / `failed` / `skipped` / `timeout` / `error`
- `RecoverHint` 枚举：`rollback` / `retry` / `escalate` / `abort` / `rewrite` / `skip`
- `StrategyResult` / `StrategyExecutor` / `ExecuteResult`
- `VerifyStrategy` 接口
