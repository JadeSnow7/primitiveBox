# PrimitiveBox Architecture Self-Review

**Document:** `docs/arch/00_arch_review.md`
**Scope:** Cross-document consistency audit of `01`–`05`
**Date:** 2026-03-16
**Method:** Systematic review against seven criteria; each finding carries a verdict, a severity, and (where applicable) a pointer to the corresponding fix applied to the source document.

---

## 一致性检查

### 检查 1：原语体系（01）与 CVR 闭环（03）的对齐程度

**结论：大体对齐，存在 3 处明确缺口，1 处概念歧义已修复。**

#### 1a. `DefaultStrategyForPrimitive()` 未覆盖 Layer 3 文档原语

**问题：**`01_primitive_taxonomy.md` 第 4 节定义了 6 个 `doc.*` 原语（`doc.read_section`、`doc.append_section`、`doc.update_section`、`doc.verify_structure`、`doc.verify_references`、`doc.verify_consistency`）。`03_cvr_loop.md` 第 2.5 节的 `DefaultStrategyForPrimitive()` 函数没有对 `doc.*` 前缀的原语做任何处理——调用者会收到 `nil` 策略，等价于跳过验证，这对有副作用的 `doc.append_section` 和 `doc.update_section` 来说是错误的默认行为。

**严重程度：** 中（设计缺口，实现前需修复）

**修复：** 已在 `03_cvr_loop.md` 中补充 `doc.*` 策略推断规则（见"已修改文档"一节）。

**推断规则（补充）：**
| 原语名称 | 推断策略 | 理由 |
|---|---|---|
| `doc.read_section` | `nil`（skip） | 只读，无副作用 |
| `doc.append_section` | `SchemaCheckStrategy{kind: heading_hierarchy}` | 写入，需验证文档结构完整性 |
| `doc.update_section` | `CompositeStrategy{AND, [SchemaCheckStrategy, ExitCodeStrategy]}` | 写入，需验证结构 + 文件已存在 |
| `doc.verify_*` | `nil`（skip） | 这些原语本身就是验证操作 |

---

#### 1b. Layer 2 代码原语的策略覆盖不完整

**问题：**`01` 定义了 7 个 `code.*` 原语。`03` 的 `DefaultStrategyForPrimitive()` 仅覆盖 `code.write_*` → `TestSuiteStrategy`，其余 5 个（`code.read_module`、`code.impact_analysis`、`code.rename`、`code.extract_function`、`code.inline_function`）没有规则。

**严重程度：** 低（`code.*` 原语本身是 P3 未实现项，但规则应一并补充）

**修复：** 已在 `03_cvr_loop.md` 中补充规则：
| 原语 | 策略 |
|---|---|
| `code.read_*`, `code.impact_analysis` | `nil`（只读） |
| `code.rename`, `code.extract_function`, `code.inline_function` | `TestSuiteStrategy`（代码变更必须验证） |

---

#### 1c. `AIPrimitive.Verify()` 与 `CVRCoordinator.VerifyStrategy` 的关系未定义（概念歧义）

**问题：**
- `01` 在 `AIPrimitive` 接口中定义了 `Verify(ctx, result) VerifyResult` 方法——这是原语的**自验证**（primitive-internal self-check）。
- `03` 在 `CVRCoordinator` 中定义了 `VerifyStrategy`——这是协调器的**外部验证**（coordinator-external validation）。

两个 verify 机制并存，但文档没有说明：
1. 对于实现了 `AIPrimitive` 的原语，`CVRCoordinator` 是否会先调用 `prim.Verify()`，再运行 `VerifyStrategy`？
2. `prim.Verify()` 的结果是否会影响 `RecoveryDecisionTree` 的输入？
3. 如果二者结果冲突（自验 pass，外验 fail），以哪个为准？

**严重程度：** 高（若不解决，实现时会产生两套并行 verify 机制，相互干扰）

**决策（已写入 03_cvr_loop.md）：**

```
两层 verify 分工明确，互不替代：

Layer A: AIPrimitive.Verify()（原语内验证）
  - 在 primitive.Execute() 返回之后、CVRCoordinator 的外部验证之前执行
  - 验证的是「原语自身的前置条件是否满足」
    例：fs.write 的 Verify() 检查目标文件是否确实被写入了正确字节数
  - 返回 VerifyResult{Passed/Failed/Skipped}
  - 若 Failed：CVRCoordinator 将结果作为 VerifyOutcomeFailed 传入 RecoveryDecisionTree

Layer B: CVRCoordinator.VerifyStrategy（外部验证）
  - 仅在 Layer A 通过（或 Layer A 返回 Skipped）后执行
  - 验证的是「业务层面的成功标准」
    例：fs.write 之后运行 go test 验证测试是否通过
  - AIPrimitive.Verify() 失败会短路 Layer B（不运行外部测试）

冲突规则：Layer A 失败 → 使用 Layer A 的结果；Layer A pass → 使用 Layer B 的结果。
对于非 AIPrimitive 的原语（只实现 Primitive 接口），Layer A = Skipped，直接运行 Layer B。
```

---

### 检查 2：Application Primitive 注册协议（02）与事件系统（04）的集成

**结论：集成在概念上正确，但存在 1 处事件重复歧义，已修复。**

#### 2a. `app.prim_called` 与 `prim.started` 的共发关系未定义

**问题：**`04` 定义了两类事件在同一个 app 原语调用时都应发出：
- `prim.started`：span 级别事件，携带 `span_id`、`trace_id`、`primitive_id`
- `app.prim_called`：app 路由级别事件，携带 `app_id`、`namespace`

这两个事件在同一次 app 原语调用中是否**都应发出**？发出顺序是什么？哪个是 span 的开始事件？文档未说明。

**严重程度：** 中（实现时容易产生重复计数或遗漏 span correlation）

**决策（已写入 04_event_observability.md）：**

```
同一次 app 原语调用发出两类事件，各司其职：

1. prim.started（由 AppRouter 发出，包含 span_id）
   → 这是 ExecutionTrace 树的 span 开始标记
   → source_system = "app"

2. app.prim_called（由 AppRouter 发出，包含 app_id + namespace）
   → 这是 AppRoute 健康/统计层面的计数事件
   → 不携带 span_id（它不是 trace 节点，只是路由计数器）

发出顺序：prim.started → [dispatch to app process] → prim.completed → app.prim_completed

app.prim_called 发出时机：在 prim.started 之后、实际 dispatch 之前。
若 AppRouter 在 dispatch 之前检测到 app 不可用（路由状态 = evicted），
只发出 app.prim_failed，不发出 prim.started（因为 span 从未开始）。
```

---

#### 2b. App 原语的 `AIPrimitive` 接口支持

**状态：正确对齐。**`02` 的 `AppPrimitiveDeclaration` 包含 `recover_strategy`、`is_reversible` 字段，这两个字段分别对应 `AIPrimitive.Recover()` 的语义和 `Reversible()` 返回值。`AppRouter` 在构建 `remotePrimitive` 时可以用这些字段填充 schema，CVRCoordinator 的 `RecoveryDecisionTree` 中的 `IsReversible` 字段直接来自此处。链路完整。

---

### 检查 3：整体架构图（05）对前四个文档的覆盖完整性

**结论：覆盖基本完整，发现 2 处遗漏，1 处依赖关系缺失，已修复。**

#### 3a. `internal/control` → `internal/cvr` 的依赖关系未在依赖矩阵中声明

**问题：**`05` 的模块边界表写道：`internal/control` 实现 `cvr.CheckpointManifestStore` 接口。这意味着 `internal/control` 需要 import `internal/cvr`（获取接口类型 `CheckpointManifestStore` 和 `CheckpointManifest` 类型）。但 `05` 的依赖矩阵中遗漏了这条 `internal/control → internal/cvr` 边。

**修复：** 已在 `05_system_architecture.md` 依赖矩阵中补充此条。

验证无循环：`internal/control` → `internal/cvr` → `internal/primitive` → `internal/eventing`（单向，无环）。`internal/cvr` 不 import `internal/control`，因此无循环。✅

---

#### 3b. `AIJudgeStrategy` 的原语调用路径未在架构图中体现

**问题：**`03` 定义了 `AIJudgeStrategy`，其执行通过调用一个「judge 原语」（如 `review.judge_output`）来实现。这意味着 `CVRCoordinator.VerifyStrategy` 执行期间会调用 `primitive.Registry.Execute()`——从 CVRCoordinator 流回到了原语层。`05` 的架构图没有画出这条从 `CVRC` 到 `REG` 的虚线箭头，容易造成「CVR 不调用原语」的误解，也掩盖了递归调用风险。

**修复：** 在 `05_system_architecture.md` 的 Mermaid 图注释中增加说明，并在依赖矩阵中注明 `AIJudgeStrategy` 通过 `PrimitiveExecutor` 接口（不直接 import Registry）调用 judge 原语，以避免循环。

**递归风险分析（见检查 5）：** 需要额外的防护机制防止 judge 原语再触发 CVR 验证。

---

#### 3c. `runtrace/trace.go` 新文件未在模块图中体现

**问题：**`04` 提出新建 `internal/runtrace/trace.go`（包含 `TraceSpan`、`ExecutionTrace`、`TraceStore`），`05` 的模块表列出了这些类型，但 Mermaid 图的 `RUNTRACE_SB` 框中未提及这些类型，新文件也未出现在任何图例中。

**严重程度：** 低（图例遗漏，不影响设计正确性）

---

## 可实现性检查

### 检查 4：第一个可以开始实现的模块

**结论：`internal/cvr/manifest.go` 是依赖最少的起点。**

#### 实现顺序与文件清单

**Step 1（零外部依赖）：** `CheckpointManifest` 类型定义
```
NEW  internal/cvr/manifest.go
     — CheckpointManifest struct
     — CheckpointReason enum (6 values)
     — CallFrame, EffectEntry, AppStateSnapshot
     — CheckpointManifestStore interface
     依赖：std lib, internal/runtrace (TraceID/SpanID strings)
```

**Step 2（依赖 Step 1）：** `VerifyStrategy` 接口与具体实现
```
NEW  internal/cvr/strategy.go
     — VerifyStrategy interface
     — StrategyResult, VerifyOutcome
     — ExitCodeStrategy
     — TestSuiteStrategy (调用 shell.exec)
     — SchemaCheckStrategy
     依赖：std lib, internal/primitive (调用 verify.test / shell.exec)
```

**Step 3（依赖 Step 1）：** `RecoveryDecisionTree`
```
NEW  internal/cvr/recovery.go
     — RecoveryContext struct
     — FailureKind string enum (8 values)
     — RecoverAction enum (6 values)
     — RecoveryDecisionTree.Decide() — 12-step
     — DecideFromVerifyHint()
     依赖：std lib only
```

**Step 4（依赖 Step 1-3）：** `CVRCoordinator`
```
NEW  internal/cvr/coordinator.go
     — CVRRequest, CVRResult
     — CheckpointPolicy, CheckpointMode
     — CVRCoordinator interface
     — cvrCoordinator implementation
     — ExecuteWithRetry() loop
     — DefaultStrategyForPrimitive()
     依赖：internal/primitive, internal/eventing, internal/runtrace, Steps 1-3
```

**Step 5（依赖 Step 1）：** SQLite schema 迁移
```
MODIFY  internal/control/sqlite_store.go
        — CREATE TABLE checkpoint_manifests (DDL from 03_cvr_loop.md §1.4)
        — StoreManifest(), GetManifest(), GetManifestChain() methods
        依赖：internal/cvr (CheckpointManifest type, CheckpointManifestStore iface)
```

**Step 6（依赖 Step 1, 4）：** `state.checkpoint` 返回 manifest
```
MODIFY  internal/primitive/state.go
        — StateCheckpoint.Execute() 创建 CheckpointManifest
        — 调用 CheckpointManifestStore.StoreManifest()（通过 context 或构造函数注入）
        注意：避免 primitive → control 的直接 import；用接口注入
```

**Step 7（依赖 Step 4）：** Engine 集成
```
MODIFY  internal/orchestrator/engine.go
        — executeStepWithRecovery() 委托给 CVRCoordinator.ExecuteWithRetry()
        — Step.CheckpointID 从 CVRResult.CheckpointManifestID 填充
        — FailureKind 从 int enum 改为 string enum（8 values）
```

**总计：** 4 个新文件，3 个修改文件。估计代码量：~1,800 行 Go（含单元测试）。

---

### 检查 5：循环依赖风险

| 风险点 | 类型 | 严重程度 | 分析 |
|---|---|---|---|
| **`AIJudgeStrategy` 调用原语** | 运行时递归 | 高 | `CVRCoordinator` 运行验证策略，策略内调用 `review.judge_output` 原语，若该原语也触发 CVRCoordinator，则形成 CVR→Verify→Primitive→CVR 无限递归。**防护措施（须在实现时加入）：** CVR 执行上下文需携带「当前递归深度」标志；`CVRCoordinator` 在创建子 context 时设置 `cvr.depth > 0`，depth > 0 时禁止再次触发 CVR 循环（直接执行原语，不 checkpoint/verify）。 |
| **`internal/control` → `internal/cvr` → `internal/primitive`** | Go import 链 | 无风险 | 单向传递，无环。`internal/cvr` 不 import `internal/control`。✅ |
| **`internal/orchestrator` 通过 `PrimitiveExecutor` 接口调用 RPC** | 运行时分层 | 低 | `Engine` 持有 `PrimitiveExecutor` 接口，实现可以是直接调用 registry 或通过 HTTP。不存在 Go import 循环，但若实现注入的是 `rpc.Server`，则 `orchestrator.Engine` 与 `rpc.Server` 在运行时形成依赖——`cmd/pb` 的 DI 需要小心不要反向注入。 |
| **`AppRouter` dispatch → App Process → pb-runtimed RPC（可能）** | 运行时递归 | 中 | App 进程内部可能再次调用 `pb-runtimed` 的 RPC 接口执行系统原语（如 `fs.read`）。这不是循环依赖，但若 App 调用自身导出的原语（间接通过 `pb-runtimed`），会产生路由环路。**防护措施：** AppRouter 应对来自 App 自身的 re-entrant call 返回 `ErrReentrantCall`，或限制调用深度。 |
| **`VerifyStrategy.Execute()` 需要 `primitive.Registry`** | Go import | 无风险 | `internal/cvr` 可以通过 `PrimitiveExecutor` 接口接受注入，而不直接 import `internal/primitive`。如此 `strategy.go` 只 import `internal/cvr`（自身包）中定义的接口。✅ |

**主动防护机制（须在实现规范中补充）：**

```go
// 在 CVRRequest 中增加递归保护字段（须在 03_cvr_loop.md 中补充）
type CVRRequest struct {
    // ... existing fields ...

    // CVRDepth is 0 for top-level calls, >0 for nested CVR invocations
    // (e.g., from AIJudgeStrategy invoking a judge primitive).
    // CVRCoordinator must not apply CVR when CVRDepth > 0.
    CVRDepth int `json:"cvr_depth,omitempty"`
}
```

---

### 检查 6：Python SDK 需要的修改

#### 需要新增的方法

| 方法 | 位置 | 说明 |
|---|---|---|
| `client.trace(label)` | `PrimitiveBoxClient` | 上下文管理器；进入时生成 `trace_id`，注入所有后续请求的 `X-PrimitiveBox-Trace-ID` header |
| `AsyncClient.trace(label)` | `AsyncPrimitiveBoxClient` | 异步版本，使用 `async with` |
| `client.get_trace(trace_id)` | `PrimitiveBoxClient` | `GET /api/v1/traces/{trace_id}` 的 Python 包装 |
| `client.replay_stream(trace_id, ...)` | `PrimitiveBoxClient` | `GET /api/v1/traces/{trace_id}/replay/stream` 的 SSE 包装 |
| `client.debug_step(span_id)` | `PrimitiveBoxClient` | `POST /api/v1/debug/step_failure` 的 Python 包装 |
| `client.checkpoints(sandbox_id)` | `PrimitiveBoxClient` | `GET /api/v1/checkpoints/{sandbox_id}` 的 Python 包装（当前只有 `state.list` wrapper） |
| `client.primitives()` | `PrimitiveBoxClient` | `GET /api/v1/primitives` 的 Python 包装（当前只有 sandbox 级的 `/primitives`） |

#### 需要修改的现有方法

| 方法 | 修改内容 | 影响 |
|---|---|---|
| `_call(method, params)` | 从 context（trace 上下文管理器）注入 `X-PrimitiveBox-Trace-ID`、`X-PrimitiveBox-Span-ID` header | 所有原语调用自动携带 trace 上下文 |
| `_call(method, params)` | 从响应头提取 `X-PrimitiveBox-Trace-Step`（已有）和新增的 `X-PrimitiveBox-Span-ID` | span 信息可供调用方读取 |
| `shell.stream_exec()` | 暴露响应 SSE 事件中的 `span_id` 字段 | 流式调用也可与 trace 关联 |
| `state.checkpoint()` | 返回结果中增加 `manifest_id` 字段的 Python 类型提示 | 对应 state.checkpoint schema 扩展 |

#### `trace()` 上下文管理器示例（设计草稿）

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-001")

# 新增：trace 上下文管理器
with client.trace("refactor-auth-handler") as trace:
    # 所有 API 调用自动携带同一个 trace_id
    client.fs.read("src/auth/handler.go")
    client.fs.write("src/auth/handler.go", content=new_content)
    result = client.shell.exec("go test ./internal/auth/...")

    print(trace.trace_id)          # 当前 trace_id
    print(trace.current_span_id)   # 最后一次调用的 span_id

# 事后查询 trace（不依赖上下文管理器）
trace_data = client.get_trace(trace.trace_id)
print(trace_data["final_status"])          # "passed"
print(len(trace_data["root_spans"]))       # 3

# 事后调试失败 span
report = client.debug_step(span_id="a1b2c3d4")
print(report["recovery"]["recommended_action"])  # "rollback"
```

---

## AI 原生性检查

### 检查 7：四维 AI 原生性评分

---

#### 7.1 原语是否真正携带语义意图？

**得分：3 / 5**

**评分依据：**

| 维度 | 现有设计 | 得分 |
|---|---|---|
| `intent` 字段 | `01` 提出在 `fs.write`、`shell.exec` 上加 mandatory `intent` 参数；当前实现不存在 | 部分 |
| `AIPrimitive.Intent()` 方法 | 接口定义了，但现有所有原语只实现 `Primitive`，不实现 `AIPrimitive` | 设计有但未落地 |
| `EstimateRisk()` 动态评估 | 定义正确；但 `risk_level` 在 `prim.started` 事件中只能是固定值，因为原语执行前无法知道实际 params 的风险 | 设计合理 |
| `DefaultScope()` | 定义了 `ScopeBoundary` 体系（file/directory/workspace/process/network/system）；比现有 `SideEffect: bool` 精细得多 | 显著改进 |
| `write_manifest` 输出 | `shell.exec` 提案中要求返回写入了哪些文件；AI 可利用这个列表理解副作用范围 | 高价值 |

**扣分原因：**
- 当前所有实现（`FSWrite`、`ShellExec` 等）均未实现 `AIPrimitive`；`intent` 字段在现有 schema 中不存在
- AI 在调用原语时必须把语义意图通过 `intent` 参数显式声明，而不是隐含在函数名称中。这依赖 LLM 能正确填写意图字符串——可靠性取决于 prompt quality，不是由框架强制的
- Layer 2/3 原语（`code.write_function`、`doc.update_section`）的 `intent` 语义更清晰，但尚未实现

**提升路径：**
- 实现 Phase 4（primitive schema amendments）后得分升至 4/5
- 推广 `AIPrimitive` 接口采用率后得分升至 5/5

---

#### 7.2 AI 失败后的恢复路径是否足够完整？

**得分：4 / 5**

**评分依据：**

| 维度 | 设计 | 得分 |
|---|---|---|
| 恢复动作数量 | 6 种（retry / rollback / fallback_earlier / rewrite / escalate / mark_unknown） | 优秀 |
| 决策树深度 | 12 步优先级决策，覆盖大多数真实失败场景 | 优秀 |
| `MarkUnknown` 语义 | verify timeout 不强制判 pass/fail，保留 checkpoint 供人工检查 | 这是关键创新 |
| `fallback_earlier` | 可回退到更早的 checkpoint（不只是直接父 checkpoint），适合多步任务中间态回滚 | 高价值 |
| `CheckpointManifest` 语义上下文 | `call_stack` + `effect_log` 帮助 AI 理解「checkpoint 时在做什么」，而不只是一个 git hash | 优秀 |
| `RecoverStrategy` 在注册协议中的声明 | App 原语在注册时声明 `recover_strategy`；Router 可据此在 App 不可用时应用对应恢复策略 | 设计一致 |

**扣分原因（-1分）：**
- `AIJudgeStrategy` 的失败不会触发自动恢复（AI judge 本身失败算 verify_error，走 retry 路径）。但 AI judge 判断为「失败」的原因（如代码质量差）并不适合 retry，应该走 rewrite。这个区分在 `RecoveryDecisionTree` 中尚未体现。
- `rewrite` 恢复动作的语义是「由 AI 重新生成参数」，但 `CVRCoordinator` 没有定义如何把「新参数」传回给 AI Agent——这需要 orchestrator 层面的 prompt 重生成协议，目前不在 CVR 设计范围内。

---

#### 7.3 系统对 AI 的行为是否足够可观测？

**得分：4 / 5**

**评分依据：**

| 维度 | 设计 | 得分 |
|---|---|---|
| 事件类型数量 | 39 种，覆盖 sandbox / prim / cvr / app 四个命名空间 | 全面 |
| 分布式 trace 关联 | `trace_id` + `span_id` + `parent_span_id` 支持树状重建 | 优秀 |
| `ExecutionTrace` 树结构 | 每个 primitive 调用是一个 span，子 primitive（如 macro.safe_edit 的子操作）是子 span | 高价值 |
| AI 调试接口 | `POST /api/v1/debug/step_failure` 返回机器可读的 `StepFailureReport`（结构化 signals + diagnosis + recovery 建议） | 高价值 |
| 两种 replay 语义 | Event stream replay（纯回放） vs Re-execution replay（重放执行），使用场景明确 | 优秀 |
| CheckpointManifest 可查询 | `GET /api/v1/checkpoints/{id}/{manifest_id}` 返回 `effect_log` + `call_stack` | 高价值 |

**扣分原因（-1分）：**
- `AIJudgeStrategy` 的判断结果（AI judge 输出的原始内容）没有进入事件流——`cvr.verify_failed` 的 payload 只有 `failure_summary` 字符串，不包含 judge 的完整推理。AI 在看到「verify failed by ai_judge」时无法了解 judge 的具体意见。
- `prim.progress` 事件目前只定义了 `stream: stdout/stderr/log`，没有包含 structured progress（例如「已处理 3/10 个文件」）——对长时间运行的 `code.impact_analysis` 等操作，进度不透明。

---

#### 7.4 Application Primitive 机制是否真正区别于 tool-calling wrapper？

**得分：4 / 5**

**对比表：**

| 特性 | 普通 tool-calling wrapper | PrimitiveBox Application Primitive |
|---|---|---|
| 注册协议 | 无（工具在 LLM prompt 中声明） | Unix socket + JSON-RPC 2.0，运行时动态注册 |
| 生命周期管理 | 无 | AppRoute 状态机（pending/active/degraded/evicted）+ 健康探针 |
| 版本控制 | 无 | `app_version` 字段；`app.prim_updated` 事件 |
| 副作用声明 | 无（工具调用黑盒） | `side_effect`、`is_reversible`、`recover_strategy` 在注册时声明 |
| CVR 集成 | 无 | App 原语自动进入 CVRCoordinator 流程（checkpoint 判断、verify 策略、recovery 决策树） |
| 可观测性 | 仅调用结果 | `app.*` 事件 + `prim.*` span（完整 trace 树） |
| 恢复语义 | 无（失败 = 重试或放弃） | 6 种 recover action，由 `recover_strategy` 声明驱动 |
| 命名空间隔离 | 无 | 强制 namespace 前缀，系统命名空间保留 |

**扣分原因（-1分）：**
- `AIPrimitive.Verify()` 的 Layer A 自验证机制（检查 1c）对 app 原语不适用——app 原语通过 Unix socket 调用，不是 Go 接口，无法实现 `AIPrimitive`。这意味着 app 原语只能享有 Layer B（外部验证策略），而无法声明「我已经自验证了」。这是一个合理的取舍，但需要在文档中明确标注。

---

## 已修改文档清单

以下修改均已直接应用到对应架构文档中。修改均为**补充性增量**，不删除或覆盖原有内容。

| 文档 | 修改位置 | 修改内容 | 原因 |
|---|---|---|---|
| `03_cvr_loop.md` | §2.5 `DefaultStrategyForPrimitive()` | 补充 `doc.*` 和 `code.*`（非 write 类）的策略推断规则 | 检查 1a、1b |
| `03_cvr_loop.md` | §2 新增 §2.6 | 说明 `AIPrimitive.Verify()` 与 `VerifyStrategy` 的分工和执行顺序 | 检查 1c |
| `03_cvr_loop.md` | §4 `CVRRequest` 结构 | 增加 `CVRDepth int` 字段及防递归规则 | 检查 5 |
| `04_event_observability.md` | §4.1.2 `app.*` 命名空间 | 补充 `app.prim_called` 与 `prim.started` 的共发顺序及语义分工 | 检查 2a |
| `05_system_architecture.md` | §5.2 依赖矩阵 | 补充 `internal/control → internal/cvr` 依赖边 | 检查 3a |
| `05_system_architecture.md` | §5.1.1 图注 | 增加 `CVRC → REG`（via PrimitiveExecutor）的 AIJudgeStrategy 路径说明 | 检查 3b |

---

## 综合评估

| 维度 | 评分 | 结论 |
|---|---|---|
| 跨文档一致性 | ★★★★☆ | 主框架一致；3 处细节缺口已修复 |
| 接口设计完整性 | ★★★★☆ | 关键接口（CVRCoordinator、VerifyStrategy、TraceStore）定义清晰；AIJudgeStrategy 递归防护待实现 |
| 可实现性 | ★★★★★ | Phase 1 起点清晰（`internal/cvr/manifest.go`）；无阻塞性循环依赖 |
| AI 语义意图表达 | ★★★☆☆ | 设计正确但未落地；Phase 4 实现后升至 ★★★★☆ |
| AI 失败恢复完整性 | ★★★★☆ | 6 种 recover action 覆盖面广；AIJudge 失败→rewrite 路径待补充 |
| AI 行为可观测性 | ★★★★☆ | 39 事件 + ExecutionTrace + AI 调试接口设计优秀；AIJudge 输出未进入 trace |
| App Primitive 原生性 | ★★★★☆ | 与 tool-calling wrapper 有本质区别；app 原语无法实现 Layer A 自验证是已知取舍 |

**最关键的实现前置动作：** 将检查 1c（`AIPrimitive.Verify()` Layer A + Layer B 分工协议）写入代码注释和接口文档，防止实现时两套 verify 机制相互干扰。
