# CVR 闭环设计：Checkpoint-Verify-Recover

> 状态：架构草案（Iteration 4 设计阶段）
> 作者：架构设计会话，2026-03-16
> 依据：internal/orchestrator/、internal/runtime/runtime.go、internal/primitive/macro.go 全量阅读

---

## 0. 现有基础与差距分析

### 现有 CVR 实现清单

| 位置 | 已实现的 CVR 能力 | 缺失 |
|------|----------------|------|
| `macro.safe_edit` | checkpoint → write → verify.test → restore on fail | 硬编码 verify.test；无策略多态；无 manifest |
| `runtime.go` execute() | `CheckpointRequired` 自动前置 checkpoint；`VerifierHint` 后置验证；`shouldRestoreAfterFailure()` | VerifierHint 是字符串非接口；验证策略单一；恢复只有 restore |
| `orchestrator/engine.go` | `executeStepWithRecovery()` retry/pause 循环 | 无 checkpoint 集成；`Step.CheckpointID` 字段存在但从不填充；恢复只有两个动作 |
| `orchestrator/recovery.go` | `FailureKind` → `ActionRetry/ActionPause` 矩阵 | 无 rollback；无 escalate；无 unknown 标记 |
| `runtrace.StepRecord` | `CheckpointID`、`VerifyResult` 字段 | 均为字符串，无结构化语义 |

### 三个核心差距

1. **Checkpoint 是字节快照，不是语义快照**：当前 checkpoint 只是一个 git commit hash，没有携带"当时正在执行什么、已产生哪些副作用"的上下文。回滚后 orchestrator 不知道应该从哪个语义状态继续。

2. **Verify 是单点检测，不是策略体系**：`VerifierHint` 指向单个原语名。文档类操作需要结构完整性检查，shell 类需要 exit code 语义，代码类需要测试套件，通用操作可能需要 AI 裁判。无法组合。

3. **Recover 是线性重试，不是决策树**：失败后只有 retry/pause，没有 rollback、escalate、unknown-preserve、fallback-to-earlier-checkpoint 等路径。可逆操作和不可逆操作用相同策略处理是错的。

---

## 1. CheckpointManifest：语义快照

### 1.1 设计原则

CheckpointManifest 是 checkpoint 的**语义元数据层**，叠加在现有 git commit 之上。

- 文件系统状态（已有）：git commit hash → `state.checkpoint` 继续负责
- 语义上下文（新增）：谁触发了这个 checkpoint、执行了哪些副作用、容器内应用的状态摘要

两者通过 `commit_hash` 关联，存储在 SQLite `checkpoint_manifests` 表中。

### 1.2 Go 类型定义

```go
package cvr

import (
    "encoding/json"
    "time"
)

// --------------------------------------------------------------------------
// CheckpointManifest — checkpoint 的语义快照
// --------------------------------------------------------------------------

// CheckpointManifest 扩展了现有的 git commit checkpoint，
// 为 recover 决策提供语义上下文。
//
// 存储：SQLite checkpoint_manifests 表（字段见 §1.4）
// 关联：通过 commit_hash 与 state.checkpoint 返回的 checkpoint_id 对应
type CheckpointManifest struct {
    // ── 身份 ──────────────────────────────────────────────────────────────

    // ManifestID 是 manifest 的唯一标识（UUID v4）。
    // 区别于 CommitHash：一个 commit 只有一个 manifest，但 manifest 可以通过
    // ManifestID 独立查询。
    ManifestID string `json:"manifest_id"`

    // CommitHash 是 state.checkpoint 返回的 checkpoint_id（git commit hash）。
    // 这是与文件系统状态的关联键。
    CommitHash string `json:"commit_hash"`

    // Label 是人类可读标签（来自 state.checkpoint params.label）。
    Label     string    `json:"label"`
    CreatedAt time.Time `json:"created_at"`

    // ── 触发上下文 ──────────────────────────────────────────────────────────

    // TriggerPrimitive 是触发此 checkpoint 的原语名（如 "fs.write"、"code.rename"）。
    // 由 CVRCoordinator 在 Execute 前设置。
    TriggerPrimitive string           `json:"trigger_primitive"`

    // TriggerReason 说明为什么触发此 checkpoint（基于 EstimateRisk 的决策）。
    TriggerReason CheckpointReason `json:"trigger_reason"`

    // ── 执行上下文 ──────────────────────────────────────────────────────────

    // TaskID / TraceID / StepID 关联到 orchestrator 的任务追踪体系。
    TaskID  string `json:"task_id,omitempty"`
    TraceID string `json:"trace_id,omitempty"`
    StepID  string `json:"step_id,omitempty"`

    // Attempt 是当前重试次数（0 = 首次执行）。
    Attempt int `json:"attempt"`

    // ── 调用栈快照 ──────────────────────────────────────────────────────────

    // CallStack 记录 checkpoint 创建时正在执行的原语调用栈。
    // 通常只有一层（当前原语），但 macro 类型可能有多层。
    // 用于 replay 时重建执行上下文。
    CallStack []CallFrame `json:"call_stack,omitempty"`

    // ── 副作用日志 ──────────────────────────────────────────────────────────

    // EffectLog 记录从上一个 checkpoint 到当前 checkpoint 之间
    // 所有已完成的副作用操作。这是 "what changed since the last safe point" 的记录。
    // 用于 recover 决策：判断需要撤销哪些操作。
    EffectLog []EffectEntry `json:"effect_log,omitempty"`

    // ── 应用状态摘要 ─────────────────────────────────────────────────────────

    // AppStates 是容器内应用（通过 AppRouter 注册）在 checkpoint 时的状态摘要。
    // 应用可选实现 primitive.state_snapshot 接口来提供此数据。
    // 为空不影响 checkpoint 有效性（应用状态摘要是可选的增强）。
    AppStates []AppStateSnapshot `json:"app_states,omitempty"`

    // ── 文件系统快照元数据 ───────────────────────────────────────────────────

    // WorkspaceRoot 是 workspace 根目录（用于 restore 时校验路径一致）。
    WorkspaceRoot string `json:"workspace_root"`

    // FilesModifiedSincePrev 是从上一个 checkpoint 到此 checkpoint 修改的文件列表。
    // 用于 recover 决策中评估回滚影响范围。
    FilesModifiedSincePrev []string `json:"files_modified_since_prev,omitempty"`

    // PrevCheckpointID 是上一个有效 checkpoint 的 commit hash。
    // 支持"回退到更早 checkpoint"的 recover 路径。
    PrevCheckpointID string `json:"prev_checkpoint_id,omitempty"`

    // ── 完整性 ────────────────────────────────────────────────────────────────

    // Corrupted 标记此 manifest 是否已损坏（无法使用）。
    // 当 state.restore 对此 checkpoint 失败时设置为 true。
    Corrupted bool   `json:"corrupted,omitempty"`
    CorruptReason string `json:"corrupt_reason,omitempty"`
}

// --------------------------------------------------------------------------
// CheckpointReason — checkpoint 触发原因枚举
// --------------------------------------------------------------------------

type CheckpointReason string

const (
    // CheckpointReasonPreEdit：写操作前的安全点（RiskMedium/High 触发）
    CheckpointReasonPreEdit CheckpointReason = "pre_edit"

    // CheckpointReasonPreRefactor：重构操作前（code.rename 等）
    CheckpointReasonPreRefactor CheckpointReason = "pre_refactor"

    // CheckpointReasonPreExec：shell 命令或危险操作前
    CheckpointReasonPreExec CheckpointReason = "pre_exec"

    // CheckpointReasonPreRestore：即将 restore 前的保险 checkpoint
    CheckpointReasonPreRestore CheckpointReason = "pre_restore"

    // CheckpointReasonManual：用户或 AI 主动调用 state.checkpoint
    CheckpointReasonManual CheckpointReason = "manual"

    // CheckpointReasonTaskBoundary：任务开始/结束时的边界 checkpoint
    CheckpointReasonTaskBoundary CheckpointReason = "task_boundary"
)

// --------------------------------------------------------------------------
// CallFrame — 调用栈帧
// --------------------------------------------------------------------------

// CallFrame 表示调用栈中的一个帧，用于记录 checkpoint 时的执行上下文。
type CallFrame struct {
    // Primitive 是正在执行的原语名
    Primitive string `json:"primitive"`

    // Depth 是调用栈深度（0 = 最顶层调用方）
    Depth int `json:"depth"`

    // ParamsDigest 是 params 的 SHA-256 前 16 字节（用于 replay 比对，不存储完整 params）
    ParamsDigest string `json:"params_digest,omitempty"`

    // EnteredAt 是进入此帧的时间
    EnteredAt time.Time `json:"entered_at"`
}

// --------------------------------------------------------------------------
// EffectEntry — 副作用日志条目
// --------------------------------------------------------------------------

// EffectEntry 记录一次已完成的副作用操作。
// 副作用日志是"从上一个 checkpoint 到当前为止发生了什么"的语义记录。
type EffectEntry struct {
    // Primitive 是产生副作用的原语名
    Primitive string `json:"primitive"`

    // EffectKind 是副作用类型
    EffectKind string `json:"effect_kind"` // "write"|"exec"|"network"|"app_mutation"

    // Summary 是对 AI 友好的副作用摘要（不超过 200 字符）
    Summary string `json:"summary"`

    // AffectedPaths 是受影响的文件路径（write 类副作用）
    AffectedPaths []string `json:"affected_paths,omitempty"`

    // AffectedSymbols 是受影响的代码符号（code 类副作用）
    AffectedSymbols []string `json:"affected_symbols,omitempty"`

    // Reversible 标记此副作用是否可通过 state.restore 撤销
    Reversible bool `json:"reversible"`

    // CompletedAt 是副作用完成时间
    CompletedAt time.Time `json:"completed_at"`
}

// --------------------------------------------------------------------------
// AppStateSnapshot — 应用状态摘要
// --------------------------------------------------------------------------

// AppStateSnapshot 是容器内应用在 checkpoint 时的可选状态摘要。
// 应用实现 state.snapshot 原语（或通过 AppServer SDK 声明 SnapshotHandler）
// 来提供此数据。
type AppStateSnapshot struct {
    // AppID 是应用的注册 ID（来自 AppRouter）
    AppID string `json:"app_id"`

    // Summary 是应用自描述的状态摘要（对 AI 友好，用于 recover 时判断是否需要重置应用状态）
    Summary string `json:"summary"`

    // Raw 是应用提供的完整状态 JSON（可选，用于深度 inspect）
    Raw json.RawMessage `json:"raw,omitempty"`

    // CapturedAt 是快照采集时间
    CapturedAt time.Time `json:"captured_at"`
}

// --------------------------------------------------------------------------
// CheckpointManifestStore — 持久化接口（由 SQLite store 实现）
// --------------------------------------------------------------------------

// CheckpointManifestStore 是 CheckpointManifest 的持久化接口。
// 由 internal/control/sqlite_store.go 实现（新增 checkpoint_manifests 表）。
type CheckpointManifestStore interface {
    // SaveManifest 持久化一个 manifest。
    SaveManifest(ctx context.Context, manifest *CheckpointManifest) error

    // GetManifest 按 ManifestID 或 CommitHash 查询 manifest。
    GetManifest(ctx context.Context, commitHashOrManifestID string) (*CheckpointManifest, error)

    // GetManifestChain 返回从指定 manifest 向前追溯的 checkpoint 链（用于 fallback recover）。
    GetManifestChain(ctx context.Context, startCommitHash string, maxDepth int) ([]*CheckpointManifest, error)

    // MarkCorrupted 标记一个 manifest 为损坏。
    MarkCorrupted(ctx context.Context, commitHash string, reason string) error
}
```

### 1.2A Intent 驱动的自动 Checkpoint 策略

为使 CVRCoordinator 能在运行时做出一致、可审计的 checkpoint 决策，`PrimitiveIntent` 不再是自由字符串，而升级为结构化策略输入。

```go
type IntentCategory string

const (
    IntentMutation     IntentCategory = "mutation"
    IntentQuery        IntentCategory = "query"
    IntentVerification IntentCategory = "verification"
    IntentRollback     IntentCategory = "rollback"
)

type RiskLevel string

const (
    RiskLow    RiskLevel = "low"
    RiskMedium RiskLevel = "medium"
    RiskHigh   RiskLevel = "high"
)

type PrimitiveIntent struct {
    Category       IntentCategory
    Reversible     bool
    RiskLevel      RiskLevel
    AffectedScopes []string
}
```

### 设计约束

`PrimitiveIntent` 是 **CVR 执行前策略决策的一部分**，不是仅供日志展示的描述字段。

协调器必须能够基于以下结构化信息自动决策：

- 这是 mutation / query / verification / rollback 中的哪一类操作
- 该操作是否可逆
- 若失败，其风险等级有多高
- 其影响资源范围是什么

因此，后续实现不得再把 `intent` 保留为自由字符串并试图在运行时做字符串解析。
面向人类的描述可以保留在文档、schema description 或审计日志中，但**运行时策略输入必须使用结构化 `PrimitiveIntent`**。

### 默认自动 checkpoint 规则

在没有显式 `VerifyStrategy` 覆盖的情况下，`CVRCoordinator` 必须根据 `PrimitiveIntent` 自动决定是否在执行前插入 checkpoint，规则如下：

| Category | Reversible | RiskLevel | 默认行为 |
|---|---|---|---|
| mutation | false | any | 执行前强制 checkpoint |
| mutation | true | high | 执行前强制 checkpoint |
| mutation | true | low/medium | 跳过 checkpoint |
| query | any | any | 跳过 checkpoint |
| verification | any | any | 跳过 checkpoint |
| rollback | any | any | 跳过 checkpoint（rollback 本身即恢复手段）|

### 策略优先级

checkpoint 决策优先级定义为：

1. 显式 `VerifyStrategy` / 协调器配置强制要求 checkpoint
2. `PrimitiveIntent` 默认策略
3. 若两者都未定义，则退回系统默认保守策略

其中：
- 显式策略可以覆盖 `PrimitiveIntent`
- 但覆盖行为必须进入 `CheckpointManifest.Trigger`
- 不允许隐式覆盖且不留痕迹

### CheckpointManifest 收敛定义

为记录 checkpoint 的真实触发原因，`CheckpointManifest` 中的触发字段收敛为：

```go
type CheckpointTrigger string

const (
    TriggerManual        CheckpointTrigger = "manual"
    TriggerIntentPolicy  CheckpointTrigger = "intent_policy"
    TriggerStrategyForce CheckpointTrigger = "strategy_forced"
)

type CheckpointManifest struct {
    ID          string
    SandboxID   string
    PrimitiveID string
    Intent      PrimitiveIntent
    Trigger     CheckpointTrigger
    CreatedAt   time.Time
    StateRef    string
}
```

其语义如下：

- `TriggerManual`：显式调用 `state.checkpoint`
- `TriggerIntentPolicy`：由 `PrimitiveIntent` 的默认策略自动触发
- `TriggerStrategyForce`：由显式 verify / coordinator 策略强制触发

### 审计与回放要求

每次自动 checkpoint 都必须记录完整的 `Intent` 快照，而不是只记录 primitive 名称。
原因是同一个 primitive 在不同参数下可能具有不同的策略含义：

- 同样是 `fs.write`，可能是低风险可逆修改，也可能是高风险不可逆变更
- 同样是 `shell.exec`，可能是只读检查，也可能是破坏性 mutation

因此：
- `CheckpointManifest.Intent` 必须保存触发当时的完整 intent 快照
- Replay / Inspector 必须能够基于该快照解释“为什么这次执行前插入了 checkpoint”

### 1.3 SQLite 新增表结构

```sql
-- 与现有 control-plane SQLite 数据库共存
CREATE TABLE IF NOT EXISTS checkpoint_manifests (
    manifest_id       TEXT PRIMARY KEY,
    commit_hash       TEXT NOT NULL UNIQUE,
    label             TEXT,
    created_at        DATETIME NOT NULL,
    trigger_primitive TEXT,
    trigger_reason    TEXT,
    task_id           TEXT,
    trace_id          TEXT,
    step_id           TEXT,
    attempt           INTEGER DEFAULT 0,
    -- JSON 列存储复杂字段（SQLite 的常规做法）
    call_stack        TEXT,   -- JSON array of CallFrame
    effect_log        TEXT,   -- JSON array of EffectEntry
    app_states        TEXT,   -- JSON array of AppStateSnapshot
    workspace_root    TEXT,
    files_modified    TEXT,   -- JSON array of strings
    prev_checkpoint_id TEXT,
    corrupted         INTEGER DEFAULT 0,
    corrupt_reason    TEXT
);
CREATE INDEX IF NOT EXISTS idx_cp_manifests_commit ON checkpoint_manifests(commit_hash);
CREATE INDEX IF NOT EXISTS idx_cp_manifests_task   ON checkpoint_manifests(task_id);
```

---

## 2. VerifyStrategy：验证策略体系

### 2.1 核心接口

```go
package cvr

import (
    "context"
    "encoding/json"

    "primitivebox/internal/primitive"
)

// --------------------------------------------------------------------------
// VerifyStrategy — 验证策略接口
// --------------------------------------------------------------------------

// StrategyExecutor 是 VerifyStrategy 调用原语的方式。
// 它是对 PrimitiveExecutor 的窄化接口，只暴露 Execute。
type StrategyExecutor interface {
    Execute(ctx context.Context, method string, params json.RawMessage) (primitive.Result, error)
}

// VerifyOutcome 枚举验证结论。
type VerifyOutcome string

const (
    VerifyOutcomePassed  VerifyOutcome = "passed"
    VerifyOutcomeFailed  VerifyOutcome = "failed"
    VerifyOutcomeSkipped VerifyOutcome = "skipped"   // 条件不满足，跳过（不算失败）
    VerifyOutcomeTimeout VerifyOutcome = "timeout"   // 验证超时，结论未知
    VerifyOutcomeError   VerifyOutcome = "error"     // 验证本身出错（不同于 failed）
)

// StrategyResult 是 VerifyStrategy.Run() 的返回值。
type StrategyResult struct {
    Outcome  VerifyOutcome `json:"outcome"`

    // Message 是对 AI 友好的一句话摘要：
    //   passed → 简洁说明通过了什么
    //   failed → 说明为什么失败、哪里不对
    Message string `json:"message"`

    // Details 是机器可读的附加信息（测试计数、lint 错误列表等）。
    Details json.RawMessage `json:"details,omitempty"`

    // RecoverHint 是此策略建议的恢复方向（供 RecoveryDecisionTree 参考）。
    RecoverHint RecoverHint `json:"recover_hint,omitempty"`

    // DurationMs 是验证耗时
    DurationMs int64 `json:"duration_ms"`
}

// VerifyStrategy 是验证策略接口。
// 每种策略封装"如何判断一次原语执行是否成功"的逻辑。
type VerifyStrategy interface {
    // Name 返回策略标识符，用于日志、事件、replay。
    Name() string

    // Description 返回对 AI 友好的策略描述。
    Description() string

    // Run 执行验证。
    //   exec: 用于调用辅助原语（如 verify.test）
    //   primitiveResult: 被验证的原语执行结果
    //   manifest: 执行前的 checkpoint manifest（用于对比上下文）
    Run(
        ctx      context.Context,
        exec     StrategyExecutor,
        result   primitive.Result,
        manifest *CheckpointManifest,
    ) (StrategyResult, error)
}

// --------------------------------------------------------------------------
// 2.2 ExitCodeStrategy
// --------------------------------------------------------------------------

// ExitCodeStrategy 验证 shell 类原语的 exit code 是否在预期范围内。
// 适用原语：shell.exec、verify.command
type ExitCodeStrategy struct {
    // ExpectedCodes 是视为成功的 exit code 列表（默认 [0]）
    ExpectedCodes []int `json:"expected_codes"`

    // FailOnStderrContent 如果 stderr 包含此字符串则额外标记失败（可选）
    FailOnStderrContent string `json:"fail_on_stderr_content,omitempty"`
}

func (s *ExitCodeStrategy) Name() string { return "exit_code" }
func (s *ExitCodeStrategy) Description() string {
    return "Checks that shell exit code is in the expected set"
}
func (s *ExitCodeStrategy) Run(
    ctx      context.Context,
    exec     StrategyExecutor,
    result   primitive.Result,
    manifest *CheckpointManifest,
) (StrategyResult, error) {
    // 从 result.Data 中提取 exit_code 字段
    // 检查 exit_code ∈ ExpectedCodes
    // 可选：检查 stderr 内容
    // 返回 StrategyResult
    return StrategyResult{}, nil // 实现省略
}

// --------------------------------------------------------------------------
// 2.3 TestSuiteStrategy
// --------------------------------------------------------------------------

// TestSuiteStrategy 运行测试命令并要求 passed=true 且 total >= MinTests。
// 适用原语：fs.write、code.write_function、code.rename（任何修改代码的原语）
type TestSuiteStrategy struct {
    // Command 是要运行的测试命令（默认 "go test ./..."）
    Command string `json:"command"`

    // Filter 是测试过滤器（仅运行相关测试）
    Filter string `json:"filter,omitempty"`

    // MinTests 是通过条件中至少需要运行的测试数量（防止空测试集误判为通过）
    MinTests int `json:"min_tests"`

    // TimeoutS 是测试超时秒数
    TimeoutS int `json:"timeout_s"`
}

func (s *TestSuiteStrategy) Name() string { return "test_suite" }
func (s *TestSuiteStrategy) Description() string {
    return "Runs test suite and requires passed=true with minimum test count"
}
func (s *TestSuiteStrategy) Run(
    ctx      context.Context,
    exec     StrategyExecutor,
    result   primitive.Result,
    manifest *CheckpointManifest,
) (StrategyResult, error) {
    // 调用 verify.test 原语（通过 exec）
    // 检查 passed=true
    // 检查 total >= MinTests
    // 返回失败时的详细信息（哪些测试失败、哪些文件）
    return StrategyResult{}, nil // 实现省略
}

// --------------------------------------------------------------------------
// 2.4 SchemaCheckStrategy
// --------------------------------------------------------------------------

// SchemaCheckStrategy 验证文档或结构化文件的完整性。
// 适用原语：doc.append_section、doc.update_section、fs.write（写文档）
type SchemaCheckStrategy struct {
    // File 是要检查的目标文件（可以从 primitiveResult 中提取，或显式指定）
    File string `json:"file,omitempty"`

    // Checks 是要执行的检查列表（空则执行所有默认检查）
    Checks []SchemaCheckKind `json:"checks,omitempty"`
}

// SchemaCheckKind 枚举文档完整性检查项。
type SchemaCheckKind string

const (
    // SchemaCheckHeadingHierarchy 检查标题层级不跳级
    SchemaCheckHeadingHierarchy SchemaCheckKind = "heading_hierarchy"

    // SchemaCheckRequiredSections 检查必需章节存在
    SchemaCheckRequiredSections SchemaCheckKind = "required_sections"

    // SchemaCheckAnchorRefs 检查内部锚点引用可解析
    SchemaCheckAnchorRefs SchemaCheckKind = "anchor_refs"

    // SchemaCheckFileRefs 检查文件引用路径存在
    SchemaCheckFileRefs SchemaCheckKind = "file_refs"

    // SchemaCheckJSONValid 检查 JSON/YAML 语法有效
    SchemaCheckJSONValid SchemaCheckKind = "json_valid"
)

func (s *SchemaCheckStrategy) Name() string { return "schema_check" }
func (s *SchemaCheckStrategy) Description() string {
    return "Verifies document structure integrity (headings, anchors, references)"
}
func (s *SchemaCheckStrategy) Run(
    ctx      context.Context,
    exec     StrategyExecutor,
    result   primitive.Result,
    manifest *CheckpointManifest,
) (StrategyResult, error) {
    // 从 primitiveResult 或 s.File 确定目标文件
    // 调用 doc.verify_structure 和 doc.verify_references
    // 汇总所有 violations
    return StrategyResult{}, nil // 实现省略
}

// --------------------------------------------------------------------------
// 2.5 AIJudgeStrategy
// --------------------------------------------------------------------------

// AIJudgeStrategy 调用另一个 AI（通过应用原语）来评判执行结果。
// 适用场景：通用内容质量、无法用代码规则表达的语义判断。
//
// 注意：AIJudge 的结果是不确定的（同一输入可能得到不同输出），
// 应与其他策略组合使用（Composite AND），而不是独立作为唯一验证。
type AIJudgeStrategy struct {
    // JudgePrimitive 是执行 AI 判断的原语名
    // （例如：应用通过 AppServer 注册的 "review.judge_output" 原语）
    JudgePrimitive string `json:"judge_primitive"`

    // CriteriaHint 是传给 AI 裁判的评判标准提示
    CriteriaHint string `json:"criteria_hint"`

    // PassThreshold 是 AI 裁判返回的评分阈值（0.0-1.0）
    // 当 JudgePrimitive 返回 score 字段时使用
    PassThreshold float64 `json:"pass_threshold,omitempty"`
}

func (s *AIJudgeStrategy) Name() string { return "ai_judge" }
func (s *AIJudgeStrategy) Description() string {
    return "Delegates quality judgment to an AI model via a registered judge primitive"
}
func (s *AIJudgeStrategy) Run(
    ctx      context.Context,
    exec     StrategyExecutor,
    result   primitive.Result,
    manifest *CheckpointManifest,
) (StrategyResult, error) {
    // 调用 s.JudgePrimitive（通过 exec），传入 result + criteria_hint
    // 从返回结果中提取 passed 或 score
    // 如果 score < PassThreshold 则失败
    return StrategyResult{}, nil // 实现省略
}

// --------------------------------------------------------------------------
// 2.6 CompositeStrategy
// --------------------------------------------------------------------------

// CompositeOperator 决定多个策略的组合逻辑。
type CompositeOperator string

const (
    // CompositeAND：所有子策略都通过才算通过
    CompositeAND CompositeOperator = "AND"

    // CompositeOR：至少一个子策略通过就算通过
    CompositeOR CompositeOperator = "OR"
)

// CompositeStrategy 将多个验证策略以 AND 或 OR 方式组合。
//
// 设计原则：
//   - AND 是严格模式（用于高风险操作）
//   - OR 是宽松模式（用于多个等效的验证手段）
//   - 嵌套 Composite 支持复杂逻辑（如 (A AND B) OR C）
type CompositeStrategy struct {
    Operator   CompositeOperator `json:"operator"`
    Strategies []VerifyStrategy  `json:"-"` // 子策略列表（运行时组装，不序列化）

    // StrategyNames 用于序列化/反序列化（replay 和 inspector 使用）
    StrategyNames []string `json:"strategy_names"`
}

func (s *CompositeStrategy) Name() string {
    return "composite_" + string(s.Operator)
}
func (s *CompositeStrategy) Description() string {
    return "Combines multiple verify strategies with " + string(s.Operator) + " logic"
}
func (s *CompositeStrategy) Run(
    ctx      context.Context,
    exec     StrategyExecutor,
    result   primitive.Result,
    manifest *CheckpointManifest,
) (StrategyResult, error) {
    // AND 模式：依次运行，第一个 failed/error/timeout 立即返回失败（短路求值）
    // OR 模式：依次运行，第一个 passed 立即返回成功（短路求值）
    // 返回所有子策略结果的汇总（Detail 字段）
    return StrategyResult{}, nil // 实现省略
}

// --------------------------------------------------------------------------
// 2.7 预设策略工厂函数
// --------------------------------------------------------------------------

// 以下是常用策略的快捷构造函数，供 CVRCoordinator 和测试使用。

// DefaultStrategyForPrimitive 根据原语名和 schema 返回推荐的默认验证策略。
// 这是 CVRCoordinator 在调用方未显式指定策略时使用的回退逻辑。
func DefaultStrategyForPrimitive(primitiveName string, schema primitive.Schema) VerifyStrategy {
    switch {
    case isShellLike(primitiveName):
        return &ExitCodeStrategy{ExpectedCodes: []int{0}}
    case isCodeMutation(primitiveName):
        // code.write_function, code.rename, code.extract_function, code.inline_function
        return &CompositeStrategy{
            Operator: CompositeAND,
            Strategies: []VerifyStrategy{
                &ExitCodeStrategy{ExpectedCodes: []int{0}},
                &TestSuiteStrategy{Command: "go test ./...", MinTests: 1, TimeoutS: 120},
            },
        }
    case isDocWriteMutation(primitiveName):
        // doc.append_section, doc.update_section
        // Verify document heading hierarchy is intact after the write.
        return &SchemaCheckStrategy{Checks: []SchemaCheckKind{SchemaCheckHeadingHierarchy, SchemaCheckRequiredSections}}
    case isDocVerify(primitiveName):
        // doc.verify_* primitives are themselves verifiers; skip external verification.
        return &skipStrategy{}
    default:
        // Read-only or side-effect-free primitives (fs.read, fs.list, fs.diff,
        // code.read_*, code.impact_analysis, doc.read_section, state.list, etc.).
        // Also covers any unknown primitives: fail safe by skipping.
        return &skipStrategy{}
    }
}

// Helper predicates used by DefaultStrategyForPrimitive.

func isShellLike(name string) bool {
    return name == "shell.exec" || name == "verify.command"
}

func isCodeMutation(name string) bool {
    return strings.HasPrefix(name, "code.write_") ||
        name == "code.rename" ||
        name == "code.extract_function" ||
        name == "code.inline_function"
}

func isDocWriteMutation(name string) bool {
    return name == "doc.append_section" || name == "doc.update_section"
}

func isDocVerify(name string) bool {
    return strings.HasPrefix(name, "doc.verify_")
}
// Note: fs.write is intentionally absent here — its strategy is caller-supplied
// (e.g., CVRRequest.VerifyStrategy = TestSuiteStrategy from macro.safe_edit).
// A bare fs.write with no caller-supplied strategy gets skipStrategy by default.
// This is intentional: fs.write alone does not know which test suite to run.
```

---

## 2.8 AIPrimitive.Verify() 与 VerifyStrategy 的分工

> **架构自检补充**（来自 `00_arch_review.md` 检查 1c）

`01_primitive_taxonomy.md` 的 `AIPrimitive` 接口定义了 `Verify(ctx, result) VerifyResult` 方法，
`CVRCoordinator` 也有独立的 `VerifyStrategy`。二者是**互补的两层验证**，不是重复或替代关系。

### 执行顺序

```
CVRCoordinator.Execute()
  │
  ├── Step 2: primitive.Execute()
  │      │
  │      └── (if primitive implements AIPrimitive)
  │             AIPrimitive.Verify()  ← Layer A：原语自验证
  │             │
  │             ├── Passed / Skipped → 继续
  │             └── Failed → 直接进入 RecoveryDecisionTree（短路 Layer B）
  │
  └── Step 3: (仅当 Layer A 通过或 Skipped 时)
         VerifyStrategy.Run()  ← Layer B：外部业务验证
         （TestSuiteStrategy / SchemaCheckStrategy / AIJudgeStrategy 等）
```

### 语义分工

| 层次 | 实现方 | 验证的问题 | 失败含义 |
|---|---|---|---|
| **Layer A** `AIPrimitive.Verify()` | 原语自身 | 「原语操作本身是否成功？」（文件写入了正确字节数？命令有输出？） | 原语执行失败，无需运行外部测试 |
| **Layer B** `VerifyStrategy` | CVRCoordinator（外部注入） | 「业务层面的成功标准是否满足？」（测试通过？文档结构完整？） | 操作完成但结果不满足业务预期 |

### 对 App 原语的限制

App 原语通过 Unix socket 分发，不是 Go 接口实现，**无法实现 `AIPrimitive`**，因此：
- App 原语只有 Layer B 验证（由注册时声明的 `verify_hint` 或调用方指定的 `VerifyStrategy`）
- `CVRCoordinator` 在处理 app 原语时直接跳过 Layer A，视为 `Skipped`

### CVRDepth 递归防护

当 Layer B 使用 `AIJudgeStrategy` 时，策略内部会调用一个 judge 原语（如 `review.judge_output`）。
若该 judge 原语本身也触发 CVRCoordinator，则形成 CVR→Verify→Primitive→CVR 递归。

防护规则：`CVRRequest.CVRDepth > 0` 时，`CVRCoordinator.Execute()` 跳过 Step 1（checkpoint）和 Step 3（verify），
直接执行原语并返回，**不再嵌套 CVR 循环**。调用方在构造嵌套请求时必须设置 `CVRDepth: parentReq.CVRDepth + 1`。

### 短路语义

为避免两层 verify 在失败路径上产生歧义，CVRCoordinator 必须遵循如下短路规则：

1. **Layer A 失败 → 短路 Layer B**
   - 当 `AIPrimitive.Verify()` 返回 `VerifyFailed` 时，CVRCoordinator **不得**继续执行 Layer B（`VerifyStrategy`）。
   - 协调器必须立即将该结果作为本次验证结论输入 `RecoveryDecisionTree`。
   - 同时在 `ExecutionTrace` 中记录：
     - `verify_short_circuit: true`
     - `verify_short_circuit_reason: "layer_a_failed"`

2. **Layer A 成功 → 继续执行 Layer B**
   - 当 `AIPrimitive.Verify()` 返回 `VerifyPassed` 时，CVRCoordinator 必须继续执行 Layer B。
   - Layer B 的结果成为本次 CVR 的最终验证结论。

3. **Layer A 未实现 → 视为通过并直接进入 Layer B**
   - 当原语未实现 `AIPrimitive` 接口时，Layer A 视为 `Skipped`。
   - `Skipped` 在控制语义上等价于「Layer A 未阻塞」，因此协调器必须直接执行 Layer B。
   - 对 app 原语也适用此规则：由于 app 原语不是 Go 接口实现，默认没有 Layer A，自然进入 Layer B。

4. **Layer A 超时 → 按失败处理并短路**
   - 若 `AIPrimitive.Verify()` 的执行时间超过该 primitive 声明的 verify/primitive timeout，则该结果必须按 `VerifyFailed` 处理。
   - 协调器不得继续执行 Layer B，必须立即进入 `RecoveryDecisionTree`。
   - 同时在 `ExecutionTrace` 中记录：
     - `verify_short_circuit: true`
     - `verify_short_circuit_reason: "layer_a_timeout"`

### ExecutionTrace 记录要求

Layer A / Layer B 的调度结果必须进入 trace，至少包含以下可查询字段：

- `layer_a_verify_outcome`: `"passed" | "failed" | "skipped" | "timeout"`
- `layer_b_executed`: `true | false`
- `verify_short_circuit`: `true | false`

其中：
- `layer_a_verify_outcome = "failed"` 或 `"timeout"` 时，`verify_short_circuit` 必须为 `true`
- `layer_a_verify_outcome = "passed"` 或 `"skipped"` 时，`layer_b_executed` 必须为 `true`

---

## 3. RecoverPolicy：失败恢复决策树

## 3.0 CVR 递归防护与 Fallback 语义

### 设计目标

`CVRDepth` 的存在不是简单的“避免无限递归”提示，而是一个**必须在 strategy 层强制执行的控制约束**。
尤其在 `AIJudgeStrategy` 通过 judge primitive 发起嵌套调用时，若没有统一的深度上限和超限 fallback 语义，不同实现会出现以下不一致行为：

- 有的实现静默跳过 verify
- 有的实现继续递归直到栈耗尽
- 有的实现把超限误判成普通 verify failed，并错误触发恢复动作

这些行为都不符合 CVR 的可验证、可回放、可审计要求。

### 默认上限

`CVRDepth` 的默认上限定义为：

```go
const DefaultCVRDepthLimit = 3
```

选择 `3` 作为默认值的理由：

1. `0` 表示顶层原语执行；
2. `1` 足以覆盖最常见的 `AIJudgeStrategy -> judge primitive` 一层嵌套；
3. `2` 允许少量受控的二级组合场景（如 composite verify 内部再触发 judge）；
4. `3` 已经为调试和扩展保留了余量，同时足够低，能快速阻断错误实现导致的递归失控。

因此，`3` 是一个兼顾可扩展性与安全边界的保守默认值。

### 超限行为

当任意 strategy（尤其是 `AIJudgeStrategy`）准备发起新的递归式 CVR 调用时，必须先检查：

- 当前深度 `Depth`
- 配置上限 `Limit`

若 `Depth >= Limit`，则必须执行以下语义：

1. **不得静默失败**
   - 实现不得返回 `nil, nil`
   - 不得把该情况伪装成普通 `VerifyFailed`
   - 不得降级为“跳过本次 judge”

2. **必须返回结构化错误**
   - 返回：
     `ErrCVRDepthExceeded{PrimitiveID, Depth, Limit}`

3. **CVRCoordinator 将其视为不可恢复失败**
   - 该错误不是业务验证失败，也不是可重试的基础设施错误
   - `CVRCoordinator` 必须将其判定为 **non-recoverable failure**
   - 一旦出现此错误，协调器**不得**再调用 `RecoveryDecisionTree`
   - 也**不得**尝试 `retry / rollback / fallback_earlier / rewrite`
   - 必须直接将该错误上报给调用方

### 检查责任边界

递归深度检查必须在 **strategy 层** 完成，而不是下沉到 primitive 执行层。

明确要求如下：

- `AIJudgeStrategy` 在调用 judge primitive 之前，必须先检查 `CVRDepth`
- `CompositeStrategy` 若会派生子 strategy 并引发递归，也必须在派生前检查
- primitive 执行层只负责执行原语，不负责推断“这次调用是否属于 CVR 递归”
- 不允许把“超限保护”隐藏在 `primitive.Execute()`、sandbox runtime、或底层 RPC proxy 中

原因是：
`CVRDepth` 是 **CVR 编排语义** 的一部分，不是 primitive 执行语义的一部分。
若把检查逻辑下沉到 primitive 层，会破坏分层边界，也会导致 trace 与恢复语义无法统一。

### ExecutionTrace 记录要求

当发生深度超限时，`ExecutionTrace` 必须显式记录：

- `cvr_depth_exceeded: true`
- `cvr_depth: <当前深度>`
- `cvr_depth_limit: <配置上限>`
- `cvr_call_stack_snapshot: [...]`

其中 `cvr_call_stack_snapshot` 必须是**当时的完整调用栈快照**，至少包含：

- 当前 primitive / strategy 名称
- 父级 primitive / strategy 链
- trace/span 关联标识（若可用）

该要求的目标是保证：
- Inspector 可以准确解释为何 CVR 被中止
- Replay 可以还原超限发生点
- 调用方可以区分“业务验证失败”与“架构保护触发”

### 协调器处理语义

`CVRCoordinator.Execute()` 收到 `ErrCVRDepthExceeded` 后，必须采用如下处理路径：

1. 标记本次执行为 failed
2. 标记失败类型为 `non_recoverable`
3. 将错误直接返回给调用方
4. 记录对应事件和 trace 字段
5. 不进入 `RecoveryDecisionTree`
6. 不执行任何恢复动作

换言之，`ErrCVRDepthExceeded` 的 fallback 语义不是“换一种恢复方式”，而是“立即停止并上报”。

### Go 类型定义

```go
package cvr

import "fmt"

// ErrCVRDepthExceeded 表示 CVR 递归深度已达到安全上限。
// 这是一个结构化、不可恢复的架构保护错误。
type ErrCVRDepthExceeded struct {
    PrimitiveID string `json:"primitive_id"`
    Depth       int    `json:"depth"`
    Limit       int    `json:"limit"`
}

func (e ErrCVRDepthExceeded) Error() string {
    return fmt.Sprintf(
        "cvr depth exceeded for primitive %q: depth=%d limit=%d",
        e.PrimitiveID,
        e.Depth,
        e.Limit,
    )
}
```

### 3.1 核心类型定义

```go
package cvr

import "time"

// --------------------------------------------------------------------------
// RecoverHint — 验证策略的恢复建议（从 VerifyStrategy 返回，供决策树参考）
// --------------------------------------------------------------------------

type RecoverHint string

const (
    RecoverHintNone              RecoverHint = ""
    RecoverHintRetry             RecoverHint = "retry"
    RecoverHintRollback          RecoverHint = "rollback"
    RecoverHintFallbackEarlier   RecoverHint = "fallback_earlier_checkpoint"
    RecoverHintEscalate          RecoverHint = "escalate"
    RecoverHintRewrite           RecoverHint = "rewrite"
    RecoverHintMarkUnknown       RecoverHint = "mark_unknown"
)

// --------------------------------------------------------------------------
// RecoverAction — 决策树的最终恢复动作
// --------------------------------------------------------------------------

// RecoverAction 是 RecoveryDecisionTree.Decide() 返回的恢复动作。
// 与现有 orchestrator.RecoveryAction（只有 RETRY/PAUSE）相比，
// 新增 Rollback、FallbackEarlier、Escalate、MarkUnknown 四个动作。
type RecoverAction string

const (
    // RecoverActionRetry：原地重试（幂等操作；环境类失败）
    RecoverActionRetry RecoverAction = "retry"

    // RecoverActionRollback：回滚到 checkpoint，然后重试（可逆操作失败）
    RecoverActionRollback RecoverAction = "rollback"

    // RecoverActionFallbackEarlier：当前 checkpoint 损坏，回退到更早的 checkpoint
    RecoverActionFallbackEarlier RecoverAction = "fallback_earlier"

    // RecoverActionRewrite：不回滚，要求 AI 重新生成参数后再试
    RecoverActionRewrite RecoverAction = "rewrite"

    // RecoverActionEscalate：需要人工介入（不可逆操作失败；max retries 超限）
    RecoverActionEscalate RecoverAction = "escalate"

    // RecoverActionMarkUnknown：保留 checkpoint，标记结果未知（verify 超时；无法判断成功/失败）
    RecoverActionMarkUnknown RecoverAction = "mark_unknown"
)

// --------------------------------------------------------------------------
// RecoveryContext — 决策树的输入
// --------------------------------------------------------------------------

// RecoveryContext 是 RecoveryDecisionTree.Decide() 的完整输入。
// 包含所有影响恢复决策的维度。
type RecoveryContext struct {
    // ── 失败信息 ──────────────────────────────────────────────────────────
    FailureKind       FailureKind    // 原语执行的失败分类（来自现有 orchestrator.FailureKind）
    VerifyOutcome     VerifyOutcome  // 验证结论（passed/failed/timeout/error/skipped）
    VerifyRecoverHint RecoverHint    // 验证策略的恢复建议

    // ── 原语语义 ──────────────────────────────────────────────────────────
    IsReversible bool   // 失败的原语是否可逆（来自 AIPrimitive.Reversible()）
    SideEffect   string // 副作用类型（"none"|"read"|"write"|"exec"）

    // ── Checkpoint 状态 ────────────────────────────────────────────────────
    HasCheckpoint       bool   // 是否存在可用的 checkpoint
    CheckpointCorrupted bool   // 当前 checkpoint 是否已损坏
    HasEarlierCheckpoint bool  // 是否存在更早的可用 checkpoint

    // ── 重试状态 ──────────────────────────────────────────────────────────
    Attempt    int // 当前已重试次数（0 = 首次执行）
    MaxRetries int // 允许的最大重试次数

    // ── 时间约束 ──────────────────────────────────────────────────────────
    // VerifyDuration 用于判断是否是 verify 超时（而非 verify 失败）
    VerifyDuration time.Duration
    VerifyTimeout  time.Duration // verify 的配置超时
}

// --------------------------------------------------------------------------
// FailureKind — 扩展现有 orchestrator.FailureKind
// --------------------------------------------------------------------------

// FailureKind 与 orchestrator.FailureKind 对齐，但使用 string 类型以便扩展。
// CVR 层使用此类型，不直接依赖 orchestrator 包的 int 枚举。
type FailureKind string

const (
    FailureKindEnvironment FailureKind = "environment"  // 依赖缺失、权限问题
    FailureKindTestFail    FailureKind = "test_fail"    // 测试断言失败
    FailureKindSyntax      FailureKind = "syntax"       // 语法/编译错误
    FailureKindTimeout     FailureKind = "timeout"      // 操作超时
    FailureKindDuplicate   FailureKind = "duplicate"    // 完全相同的重试
    FailureKindAppUnavailable FailureKind = "app_unavailable" // 应用原语不可达
    FailureKindVerifyTimeout  FailureKind = "verify_timeout"  // 验证本身超时（区别于执行超时）
    FailureKindUnknown     FailureKind = "unknown"
)

// --------------------------------------------------------------------------
// RecoveryDecisionTree — 决策树
// --------------------------------------------------------------------------

// RecoveryDecisionTree 实现 CVR 的恢复决策逻辑。
// 取代现有 orchestrator.RecoveryPolicy 的简单矩阵，提供树形决策路径。
type RecoveryDecisionTree struct{}

// NewRecoveryDecisionTree 创建默认决策树实例。
func NewRecoveryDecisionTree() *RecoveryDecisionTree {
    return &RecoveryDecisionTree{}
}

// Decide 根据 RecoveryContext 返回恢复动作。
//
// 决策树逻辑（按优先级顺序）：
//
// ① max retries 超限 → Escalate
// ② verify 超时 → MarkUnknown
// ③ checkpoint 损坏 + 有更早 checkpoint → FallbackEarlier
// ④ checkpoint 损坏 + 无更早 checkpoint → Escalate
// ⑤ 可逆操作失败 + 有 checkpoint → Rollback
// ⑥ 不可逆操作失败 → Escalate
// ⑦ 环境类失败（依赖缺失）→ Retry（短暂等待后）
// ⑧ 应用不可用 → Retry（等待健康检查恢复）
// ⑨ 语法错误 → Rewrite（让 AI 重新生成）
// ⑩ 测试失败 + 有 checkpoint → Rollback（回到已知好状态）
// ⑪ 重复重试（duplicate）→ Escalate（陷入无效循环）
// ⑫ 其他 → Retry（直到 max retries）
func (d *RecoveryDecisionTree) Decide(ctx RecoveryContext) RecoverAction {
    // ① 最高优先级：max retries 超限
    if ctx.Attempt >= ctx.MaxRetries {
        return RecoverActionEscalate
    }

    // ② verify 超时（结论未知）
    if ctx.VerifyOutcome == VerifyOutcomeTimeout ||
        ctx.FailureKind == FailureKindVerifyTimeout {
        return RecoverActionMarkUnknown
    }

    // ③④ checkpoint 损坏
    if ctx.CheckpointCorrupted {
        if ctx.HasEarlierCheckpoint {
            return RecoverActionFallbackEarlier
        }
        return RecoverActionEscalate
    }

    // ⑤ 可逆操作失败 + 有 checkpoint → rollback
    if ctx.IsReversible && ctx.HasCheckpoint &&
        (ctx.VerifyOutcome == VerifyOutcomeFailed ||
            ctx.FailureKind == FailureKindTestFail) {
        return RecoverActionRollback
    }

    // ⑥ 不可逆操作失败（网络请求、数据库写入等）
    if !ctx.IsReversible &&
        (ctx.VerifyOutcome == VerifyOutcomeFailed ||
            ctx.FailureKind == FailureKindTestFail) {
        return RecoverActionEscalate
    }

    // ⑦ 环境类失败
    if ctx.FailureKind == FailureKindEnvironment {
        return RecoverActionRetry
    }

    // ⑧ 应用原语不可用（等待健康检查恢复）
    if ctx.FailureKind == FailureKindAppUnavailable {
        return RecoverActionRetry
    }

    // ⑨ 语法错误（让 AI 重新生成，不回滚）
    if ctx.FailureKind == FailureKindSyntax {
        return RecoverActionRewrite
    }

    // ⑩ 测试失败且有 checkpoint（情况已在 ⑤ 覆盖，此处处理无 checkpoint 的情况）
    if ctx.FailureKind == FailureKindTestFail {
        if ctx.HasCheckpoint {
            return RecoverActionRollback
        }
        return RecoverActionEscalate
    }

    // ⑪ 重复重试（陷入无效循环）
    if ctx.FailureKind == FailureKindDuplicate {
        return RecoverActionEscalate
    }

    // ⑫ 兜底：retry
    return RecoverActionRetry
}

// DecideFromVerifyHint 将 VerifyStrategy 的 RecoverHint 转换为最终决策。
// 当 RecoverHint 明确时，优先采用 hint 而非全量决策树（用于应用原语的 recover_strategy 声明）。
func (d *RecoveryDecisionTree) DecideFromVerifyHint(
    hint    RecoverHint,
    ctx     RecoveryContext,
) RecoverAction {
    if ctx.Attempt >= ctx.MaxRetries {
        return RecoverActionEscalate
    }
    switch hint {
    case RecoverHintRollback:
        if ctx.HasCheckpoint {
            return RecoverActionRollback
        }
        return RecoverActionEscalate
    case RecoverHintRetry:
        return RecoverActionRetry
    case RecoverHintRewrite:
        return RecoverActionRewrite
    case RecoverHintEscalate:
        return RecoverActionEscalate
    case RecoverHintMarkUnknown:
        return RecoverActionMarkUnknown
    case RecoverHintFallbackEarlier:
        if ctx.HasEarlierCheckpoint {
            return RecoverActionFallbackEarlier
        }
        return RecoverActionEscalate
    default:
        return d.Decide(ctx)
    }
}
```

---

## 4. CVRCoordinator：协调器

### 4.1 接口设计

```go
package cvr

import (
    "context"
    "encoding/json"
    "time"

    "primitivebox/internal/eventing"
    "primitivebox/internal/primitive"
)

// --------------------------------------------------------------------------
// CVRRequest — 协调器的执行请求
// --------------------------------------------------------------------------

// CVRRequest 描述一次通过 CVRCoordinator 执行的原语调用，
// 包含执行参数、验证策略和恢复配置。
type CVRRequest struct {
    // ── 执行参数 ──────────────────────────────────────────────────────────
    Primitive string          `json:"primitive"`
    Params    json.RawMessage `json:"params"`

    // ── 上下文关联 ──────────────────────────────────────────────────────────
    TaskID  string `json:"task_id,omitempty"`
    TraceID string `json:"trace_id,omitempty"`
    StepID  string `json:"step_id,omitempty"`
    Attempt int    `json:"attempt"`

    // ── 验证策略 ──────────────────────────────────────────────────────────

    // VerifyStrategy 是本次调用使用的验证策略。
    // nil 表示使用 DefaultStrategyForPrimitive() 自动推断。
    VerifyStrategy VerifyStrategy `json:"-"`

    // VerifyTimeout 是验证的超时时间（默认 120s）。
    VerifyTimeout time.Duration `json:"verify_timeout_s"`

    // ── 检查点策略 ──────────────────────────────────────────────────────────

    // CheckpointPolicy 决定何时自动插入 checkpoint。
    // nil 表示使用默认策略（基于 EstimateRisk）。
    CheckpointPolicy *CheckpointPolicy `json:"checkpoint_policy,omitempty"`

    // CheckpointLabel 是自动插入 checkpoint 时的标签。
    // 默认格式："{primitive}-{timestamp}"
    CheckpointLabel string `json:"checkpoint_label,omitempty"`

    // ── 恢复配置 ──────────────────────────────────────────────────────────
    MaxRetries int `json:"max_retries"` // 默认 3

    // CVRDepth 防止递归 CVR 调用（如 AIJudgeStrategy 内部调用原语时）。
    // 顶层调用为 0；CVRCoordinator 在传递给子调用时自动设置为 depth+1。
    // 当 CVRDepth > 0 时，CVRCoordinator 不执行 checkpoint/verify 步骤，
    // 直接执行原语并返回，防止 CVR→Verify→Primitive→CVR 无限递归。
    CVRDepth int `json:"cvr_depth,omitempty"`
}

// --------------------------------------------------------------------------
// CheckpointPolicy — checkpoint 插入策略
// --------------------------------------------------------------------------

// CheckpointPolicy 控制 CVRCoordinator 何时在执行前插入 checkpoint。
type CheckpointPolicy struct {
    // Mode 决定插入策略
    Mode CheckpointMode `json:"mode"`

    // MinRiskLevel：仅当 EstimateRisk >= MinRiskLevel 时插入（Mode=AutoRisk 时使用）
    MinRiskLevel primitive.RiskLevel `json:"min_risk_level,omitempty"`
}

// CheckpointMode 枚举 checkpoint 插入模式。
type CheckpointMode string

const (
    // CheckpointModeAlways：总是在执行前插入 checkpoint。
    CheckpointModeAlways CheckpointMode = "always"

    // CheckpointModeAutoRisk：当 EstimateRisk >= MinRiskLevel 时插入（默认行为）。
    CheckpointModeAutoRisk CheckpointMode = "auto_risk"

    // CheckpointModeNever：不自动插入 checkpoint（用于只读操作的显式优化）。
    CheckpointModeNever CheckpointMode = "never"
)

// DefaultCheckpointPolicy 是 CVRCoordinator 的默认 checkpoint 策略：
// 当风险等级 >= RiskMedium 时自动插入 checkpoint。
var DefaultCheckpointPolicy = &CheckpointPolicy{
    Mode:         CheckpointModeAutoRisk,
    MinRiskLevel: primitive.RiskMedium,
}

// --------------------------------------------------------------------------
// CVRResult — 协调器的执行结果
// --------------------------------------------------------------------------

// CVRResult 封装一次 CVR 完整执行的结果，包括原语结果、验证结果和恢复信息。
type CVRResult struct {
    // ── 原语执行 ──────────────────────────────────────────────────────────
    PrimitiveResult primitive.Result `json:"primitive_result"`
    ExecutionError  error            `json:"-"` // 原语执行错误（不序列化）

    // ── Checkpoint ──────────────────────────────────────────────────────────
    CheckpointTaken bool   `json:"checkpoint_taken"`
    CheckpointID    string `json:"checkpoint_id,omitempty"` // 执行前的 checkpoint
    Manifest        *CheckpointManifest `json:"-"`          // 完整 manifest

    // ── 验证 ──────────────────────────────────────────────────────────────
    VerifyStrategy string         `json:"verify_strategy,omitempty"`
    VerifyResult   StrategyResult `json:"verify_result"`

    // ── 恢复 ──────────────────────────────────────────────────────────────
    RecoverAction   RecoverAction `json:"recover_action,omitempty"`
    RolledBack      bool          `json:"rolled_back"`
    RolledBackTo    string        `json:"rolled_back_to,omitempty"` // checkpoint hash
    EscalationMsg   string        `json:"escalation_msg,omitempty"`

    // ── 整体结论 ──────────────────────────────────────────────────────────
    // Passed = 原语执行成功 AND 验证通过（或跳过）AND 无恢复失败
    Passed     bool          `json:"passed"`
    DurationMs int64         `json:"duration_ms"`
    Attempt    int           `json:"attempt"`
}

// --------------------------------------------------------------------------
// CVRCoordinator — 协调器主接口
// --------------------------------------------------------------------------

// CVRCoordinator 负责将 Checkpoint、Verify、Recover 三个步骤编排为闭环。
//
// 设计层级：
//   Level 0（原语级）：runtime.go 的 CheckpointRequired + VerifierHint（已有，保持不变）
//   Level 1（步骤级）：CVRCoordinator（本设计，orchestrator 中每个 Step 的 CVR 包装层）
//   Level 2（任务级）：未来可在 TaskBoundary 插入全局 checkpoint/verify
//
// CVRCoordinator 不替换 Level 0；两层互补且各司其职。
type CVRCoordinator interface {
    // Execute 执行一次完整的 CVR 循环。
    //
    // 完整步骤：
    //   1. EstimateRisk(params) → 决定是否需要 checkpoint
    //   2. 如需要：state.checkpoint → 保存 CheckpointManifest
    //   3. 执行原语（通过 executor）
    //   4. 运行 VerifyStrategy（如有配置）
    //   5. 根据验证结果 + RecoveryDecisionTree 决定恢复动作
    //   6. 执行恢复动作（rollback / escalate / mark_unknown 等）
    //   7. 发布 CVR 事件（cvr.checkpoint_taken / cvr.verify_* / cvr.recover_*）
    //   8. 返回 CVRResult
    Execute(ctx context.Context, req CVRRequest) (CVRResult, error)

    // ExecuteWithRetry 包装 Execute，按 MaxRetries 自动循环直到 Passed=true 或决策 Escalate。
    // 每次重试前，CVRResult.RecoverAction 决定重试前的操作。
    ExecuteWithRetry(ctx context.Context, req CVRRequest) (CVRResult, error)
}

// --------------------------------------------------------------------------
// cvrCoordinator — CVRCoordinator 的默认实现
// --------------------------------------------------------------------------

type cvrCoordinator struct {
    executor       StrategyExecutor        // 原语执行器（来自 runtime.Runtime）
    checkpointer   primitive.Primitive     // state.checkpoint 原语
    restorer       primitive.Primitive     // state.restore 原语
    manifestStore  CheckpointManifestStore // CheckpointManifest 持久化
    decisionTree   *RecoveryDecisionTree   // 恢复决策树
    eventBus       *eventing.Bus           // 事件发布（write-and-emit 规则）
}

// NewCVRCoordinator 构建 CVRCoordinator 实例。
// 参数由 internal/runtime/runtime.go 的 New() 函数注入。
func NewCVRCoordinator(
    executor      StrategyExecutor,
    checkpointer  primitive.Primitive,
    restorer      primitive.Primitive,
    manifestStore CheckpointManifestStore,
    bus           *eventing.Bus,
) CVRCoordinator {
    return &cvrCoordinator{
        executor:      executor,
        checkpointer:  checkpointer,
        restorer:      restorer,
        manifestStore: manifestStore,
        decisionTree:  NewRecoveryDecisionTree(),
        eventBus:      bus,
    }
}

// Execute 实现完整的 CVR 循环（内部实现说明，伪代码注释）。
func (c *cvrCoordinator) Execute(ctx context.Context, req CVRRequest) (CVRResult, error) {
    result := CVRResult{Attempt: req.Attempt}
    start := time.Now()

    // ── Step 1: 风险评估 & Checkpoint 决策 ────────────────────────────────
    //
    // 1a. 获取原语 schema（从 executor 的 registry 查询）
    // 1b. 调用 AIPrimitive.EstimateRisk(req.Params)（如果原语实现了 AIPrimitive）
    //     否则退化到 schema.SideEffect 的保守估算
    // 1c. 按 CheckpointPolicy 决定是否插入 checkpoint
    //
    // 伪代码：
    //   risk := estimateRisk(req.Primitive, req.Params, schema)
    //   if policy.ShouldCheckpoint(risk) {
    //       cp, manifest := c.takeCheckpoint(ctx, req)
    //       result.CheckpointTaken = true
    //       result.CheckpointID = cp.CheckpointID
    //       result.Manifest = manifest
    //   }

    // ── Step 2: 执行原语 ───────────────────────────────────────────────────
    //
    //   primitiveResult, execErr := c.executor.Execute(ctx, req.Primitive, req.Params)
    //   result.PrimitiveResult = primitiveResult
    //   result.ExecutionError = execErr
    //   c.appendEffectEntry(result.Manifest, req.Primitive, primitiveResult)
    //   c.emitEvent(ctx, "cvr.primitive_executed", ...)

    // ── Step 3: Verify ─────────────────────────────────────────────────────
    //
    //   strategy := req.VerifyStrategy
    //   if strategy == nil {
    //       strategy = DefaultStrategyForPrimitive(req.Primitive, schema)
    //   }
    //   if execErr == nil && strategy.Name() != "skip" {
    //       c.emitEvent(ctx, "cvr.verify_started", strategy.Name())
    //       verifyResult, verifyErr := runWithTimeout(strategy.Run, req.VerifyTimeout)
    //       result.VerifyStrategy = strategy.Name()
    //       result.VerifyResult = verifyResult
    //       c.emitEvent(ctx, "cvr.verify_"+string(verifyResult.Outcome), ...)
    //   }

    // ── Step 4: 恢复决策 ────────────────────────────────────────────────────
    //
    //   if execErr != nil || result.VerifyResult.Outcome == VerifyOutcomeFailed {
    //       recovCtx := buildRecoveryContext(req, result, schema)
    //       action := c.decisionTree.Decide(recovCtx)
    //       result.RecoverAction = action
    //       c.emitEvent(ctx, "cvr.recover_started", action)
    //       c.executeRecoverAction(ctx, action, result)
    //       c.emitEvent(ctx, "cvr.recover_completed", action)
    //   }

    // ── Step 5: 设置 Passed 并返回 ─────────────────────────────────────────
    //
    //   result.Passed = (execErr == nil) &&
    //       (result.VerifyResult.Outcome == VerifyOutcomePassed ||
    //        result.VerifyResult.Outcome == VerifyOutcomeSkipped)
    //   result.DurationMs = time.Since(start).Milliseconds()
    //   return result, nil

    result.DurationMs = time.Since(start).Milliseconds()
    return result, nil // 实现省略
}

func (c *cvrCoordinator) ExecuteWithRetry(ctx context.Context, req CVRRequest) (CVRResult, error) {
    maxRetries := req.MaxRetries
    if maxRetries <= 0 {
        maxRetries = 3
    }

    var lastResult CVRResult
    for attempt := 0; attempt <= maxRetries; attempt++ {
        req.Attempt = attempt
        result, err := c.Execute(ctx, req)
        if err != nil {
            return result, err
        }
        lastResult = result

        if result.Passed {
            return result, nil
        }

        // 按 RecoverAction 决定是否继续重试
        switch result.RecoverAction {
        case RecoverActionEscalate, RecoverActionMarkUnknown:
            // 终止循环，返回当前结果
            return result, nil
        case RecoverActionRollback, RecoverActionRetry, RecoverActionRewrite:
            // rollback 已在 Execute 内部执行，继续循环
            continue
        case RecoverActionFallbackEarlier:
            // fallback 已在 Execute 内部执行，继续循环（从更早 checkpoint 重试）
            continue
        }
    }
    return lastResult, nil
}
```

---

## 5. 完整执行流程图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         CVRCoordinator.ExecuteWithRetry()                        │
│                                                                                  │
│  attempt=0,1,...,maxRetries                                                      │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                       CVRCoordinator.Execute()                           │    │
│  │                                                                          │    │
│  │  ┌──────────────────────────────────────────────────────────────────┐   │    │
│  │  │  Step 1: RISK ASSESSMENT & CHECKPOINT DECISION                    │   │    │
│  │  │                                                                   │   │    │
│  │  │  AIPrimitive.EstimateRisk(params)                                 │   │    │
│  │  │         │                                                         │   │    │
│  │  │  ┌──────▼──────────────────┐                                      │   │    │
│  │  │  │ risk >= MinRiskLevel?   │                                      │   │    │
│  │  │  │ (CheckpointPolicy)      │                                      │   │    │
│  │  │  └─────────────────────────┘                                      │   │    │
│  │  │          │ YES                      │ NO                          │   │    │
│  │  │          ▼                          ▼                             │   │    │
│  │  │  state.checkpoint()         skip checkpoint                       │   │    │
│  │  │  SaveManifest(manifest)     manifest = nil                        │   │    │
│  │  │  emit: cvr.checkpoint_taken                                       │   │    │
│  │  │          │                          │                             │   │    │
│  │  └──────────┼──────────────────────────┼─────────────────────────────┘   │    │
│  │             │                          │                                  │    │
│  │  ┌──────────▼──────────────────────────▼─────────────────────────────┐   │    │
│  │  │  Step 2: PRIMITIVE EXECUTION                                       │   │    │
│  │  │                                                                   │   │    │
│  │  │  executor.Execute(ctx, primitive, params)                         │   │    │
│  │  │          │                                                        │   │    │
│  │  │  append EffectEntry to manifest.EffectLog                         │   │    │
│  │  │  emit: cvr.primitive_executed                                     │   │    │
│  │  │          │                                                        │   │    │
│  │  │  ┌───────▼───────────────┐                                        │   │    │
│  │  │  │  execErr != nil?      │                                        │   │    │
│  │  │  └───────────────────────┘                                        │   │    │
│  │  │          │ YES                       │ NO                         │   │    │
│  │  │          ▼                           ▼                            │   │    │
│  │  │  skip verify                  proceed to verify                   │   │    │
│  │  │  failureKind = classify(err)                                      │   │    │
│  │  └──────────┬────────────────────────────┬──────────────────────────┘   │    │
│  │             │                            │                               │    │
│  │  ┌──────────▼────────────────────────────▼──────────────────────────┐   │    │
│  │  │  Step 3: VERIFY                                                   │   │    │
│  │  │                                                                   │   │    │
│  │  │  strategy = req.VerifyStrategy                                    │   │    │
│  │  │          │ nil                                                    │   │    │
│  │  │          ▼                                                        │   │    │
│  │  │  DefaultStrategyForPrimitive(primitive, schema)                   │   │    │
│  │  │          │                                                        │   │    │
│  │  │          ▼                                                        │   │    │
│  │  │  emit: cvr.verify_started                                         │   │    │
│  │  │          │                                                        │   │    │
│  │  │  runWithTimeout(strategy.Run, VerifyTimeout)                      │   │    │
│  │  │          │                                                        │   │    │
│  │  │  ┌───────▼────────────────────────────────────────────────────┐  │   │    │
│  │  │  │  VerifyOutcome?                                             │  │   │    │
│  │  │  │  passed  → emit: cvr.verify_passed  → Step 4 (no action)  │  │   │    │
│  │  │  │  skipped → emit: cvr.verify_skipped → Step 4 (no action)  │  │   │    │
│  │  │  │  failed  → emit: cvr.verify_failed  → Step 4 (recover)    │  │   │    │
│  │  │  │  timeout → emit: cvr.verify_timeout → Step 4 (recover)    │  │   │    │
│  │  │  │  error   → emit: cvr.verify_error   → Step 4 (recover)    │  │   │    │
│  │  │  └────────────────────────────────────────────────────────────┘  │   │    │
│  │  └──────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                          │    │
│  │  ┌──────────────────────────────────────────────────────────────────┐   │    │
│  │  │  Step 4: RECOVER DECISION                                         │   │    │
│  │  │                                                                   │   │    │
│  │  │  RecoveryDecisionTree.Decide(RecoveryContext{                     │   │    │
│  │  │      FailureKind, VerifyOutcome, IsReversible,                    │   │    │
│  │  │      HasCheckpoint, CheckpointCorrupted, ...})                    │   │    │
│  │  │                    │                                              │   │    │
│  │  │  ┌─────────────────▼────────────────────────────────────────┐    │   │    │
│  │  │  │  RecoverAction?                                           │    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  retry         → return CVRResult{Passed:false}           │    │   │    │
│  │  │  │                  ExecuteWithRetry 继续下一次 attempt       │    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  rollback      → state.restore(checkpointID)             │    │   │    │
│  │  │  │                  manifest.appendRestoreEffect()           │    │   │    │
│  │  │  │                  return CVRResult{RolledBack:true}        │    │   │    │
│  │  │  │                  ExecuteWithRetry 继续下一次 attempt       │    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  fallback_     → GetManifestChain(start, depth)          │    │   │    │
│  │  │  │  earlier         找到最近未损坏 checkpoint                 │    │   │    │
│  │  │  │                  state.restore(earlierCheckpointID)       │    │   │    │
│  │  │  │                  继续下一次 attempt                        │    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  rewrite       → return CVRResult{Passed:false}           │    │   │    │
│  │  │  │                  ExecuteWithRetry 继续（AI 重新生成 params）│    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  mark_unknown  → return CVRResult{Passed:false,          │    │   │    │
│  │  │  │                    RecoverAction:mark_unknown}            │    │   │    │
│  │  │  │                  ExecuteWithRetry 终止循环                 │    │   │    │
│  │  │  │                                                           │    │   │    │
│  │  │  │  escalate      → return CVRResult{EscalationMsg:...}     │    │   │    │
│  │  │  │                  ExecuteWithRetry 终止循环                 │    │   │    │
│  │  │  └───────────────────────────────────────────────────────────┘    │   │    │
│  │  └──────────────────────────────────────────────────────────────────┘   │    │
│  │                                                                          │    │
│  └──────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘


典型成功路径（code.write_function，RiskMedium）：

  attempt=0
    │
    ├─[1] EstimateRisk = RiskMedium
    │     state.checkpoint("pre-write_function-1234")
    │     SaveManifest({trigger:"pre_edit", callStack:[...], effectLog:[]})
    │     emit: cvr.checkpoint_taken
    │
    ├─[2] code.write_function.Execute(params)
    │     → {action:"replaced", file:"handler.go", syntax_ok:true}
    │     append EffectEntry{kind:"write", paths:["handler.go"], reversible:true}
    │     emit: cvr.primitive_executed
    │
    ├─[3] strategy = CompositeAND[ExitCode, TestSuite]
    │     ExitCodeStrategy.Run() → passed
    │     TestSuiteStrategy.Run() → "go test ./..." → passed(12/12)
    │     emit: cvr.verify_passed
    │
    ├─[4] no recovery needed
    │
    └─[5] CVRResult{Passed:true, CheckpointID:"abc123", VerifyResult:{passed,12 tests}}


典型失败→回滚路径（code.rename，RiskHigh，测试失败）：

  attempt=0
    ├─[1] EstimateRisk = RiskHigh → checkpoint
    ├─[2] code.rename.Execute → renamed 23 symbols
    ├─[3] TestSuiteStrategy → failed(2/15, compilation error in test_foo.go)
    │     emit: cvr.verify_failed
    ├─[4] Decide({TestFail, isReversible:true, hasCheckpoint:true})
    │     → RecoverActionRollback
    │     state.restore("def456")
    │     RolledBack=true, RolledBackTo="def456"
    │     emit: cvr.recover_completed
    └─    return CVRResult{Passed:false, RolledBack:true, RecoverAction:rollback}
  attempt=1
    ├─[1] checkpoint（新的 attempt，重新 checkpoint）
    ├─[2] code.rename（AI 已调整策略或修正了受影响文件）
    ├─[3] TestSuiteStrategy → passed(15/15)
    └─    CVRResult{Passed:true}


verify 超时路径（AIJudgeStrategy 超时）：

  attempt=0
    ├─[2] some_mutation.Execute → OK
    ├─[3] AIJudgeStrategy → timeout（30s）
    │     emit: cvr.verify_timeout
    ├─[4] Decide({VerifyTimeout}) → RecoverActionMarkUnknown
    └─    CVRResult{Passed:false, RecoverAction:mark_unknown}
          ExecuteWithRetry 终止循环
          orchestrator 将 Step 标记为 StepStatus="UNKNOWN"
          checkpoint 保留，人工 inspect 后决定是否接受
```

---

## 6. 与现有 internal/ 代码的集成点

### 6.1 `internal/orchestrator/engine.go` — 主要集成点

**当前**：`executeStepWithRecovery()` 直接调用 `e.executor.Execute()`，recovery 仅 retry/pause。

**目标**：`executeStepWithRecovery()` 改为调用 `CVRCoordinator.ExecuteWithRetry()`，传入从 Step 配置派生的 `CVRRequest`。

```go
// 修改前（engine.go:83）
result, err := e.executor.Execute(ctx, step.Primitive, step.Input)

// 修改后
cvrReq := CVRRequest{
    Primitive:      step.Primitive,
    Params:         step.Input,
    TaskID:         task.ID,
    StepID:         step.ID,
    Attempt:        attempt,
    MaxRetries:     maxRetries,
    VerifyStrategy: step.VerifyStrategy, // Step 新增字段
    CheckpointLabel: fmt.Sprintf("%s-step-%s", task.ID, step.ID),
}
cvrResult, err := e.coordinator.ExecuteWithRetry(ctx, cvrReq)
// 从 cvrResult 填充 step.CheckpointID、step.Result 等字段
```

**新增 Step 字段**（`executor.go` 中的 `Step` struct）：
```go
type Step struct {
    // ... 现有字段 ...
    VerifyStrategy VerifyStrategy `json:"-"` // 新增：步骤级验证策略
    CVRResult      *CVRResult     `json:"cvr_result,omitempty"` // 新增：CVR 完整结果
}
```

### 6.2 `internal/runtime/runtime.go` — 兼容保留

runtime.go 的 Level 0 CVR（`CheckpointRequired` + `VerifierHint`）**保持不变**。

CVRCoordinator 在 `runtime.New()` 中构建并注入到 Engine：

```go
// runtime.go 的 New() 函数中新增：
coordinator := cvr.NewCVRCoordinator(
    rt,                // StrategyExecutor（rt.Execute 实现此接口）
    rt.checkpointer,   // state.checkpoint
    rt.restorer,       // state.restore
    manifestStore,     // CheckpointManifestStore（来自 control.SQLiteStore 的扩展）
    eventBus,          // eventing.Bus
)
```

**两层 CVR 的关系**：

| 层级 | 位置 | 粒度 | 触发方式 |
|------|------|------|---------|
| Level 0（原语级） | runtime.execute() | 单个原语 | schema.CheckpointRequired |
| Level 1（步骤级） | CVRCoordinator | 原语 + verify + recover | orchestrator.Step 配置 |

Level 0 负责"原语自身声明需要 checkpoint"的简单场景（保持现有行为）。
Level 1 负责"orchestrator 任务步骤的完整 CVR 闭环"。两者共用同一个 `state.checkpoint` 原语，不重复实现。

### 6.3 `internal/control/sqlite_store.go` — 新增 manifest 存储

实现 `CheckpointManifestStore` 接口，在现有 SQLite 数据库中新增 `checkpoint_manifests` 表（见 §1.3 SQL）。

`SQLiteStore` 同时实现 `control.Store` 和 `CheckpointManifestStore`：

```go
// sqlite_store.go 新增方法：
func (s *SQLiteStore) SaveManifest(ctx context.Context, m *cvr.CheckpointManifest) error
func (s *SQLiteStore) GetManifest(ctx context.Context, id string) (*cvr.CheckpointManifest, error)
func (s *SQLiteStore) GetManifestChain(ctx context.Context, start string, depth int) ([]*cvr.CheckpointManifest, error)
func (s *SQLiteStore) MarkCorrupted(ctx context.Context, hash, reason string) error
```

### 6.4 `internal/eventing/` — 新增 CVR 事件类型

在 eventing 的事件类型常量中新增 CVR 事件（遵循 write-and-emit 规则）：

```go
// 新增事件类型常量（eventing.go 或新建 cvr_events.go）
const (
    EventCVRCheckpointTaken  = "cvr.checkpoint_taken"
    EventCVRPrimitiveExecuted= "cvr.primitive_executed"
    EventCVRVerifyStarted    = "cvr.verify_started"
    EventCVRVerifyPassed     = "cvr.verify_passed"
    EventCVRVerifyFailed     = "cvr.verify_failed"
    EventCVRVerifyTimeout    = "cvr.verify_timeout"
    EventCVRVerifyError      = "cvr.verify_error"
    EventCVRVerifySkipped    = "cvr.verify_skipped"
    EventCVRRecoverStarted   = "cvr.recover_started"
    EventCVRRecoverCompleted = "cvr.recover_completed"
    EventCVRPassed           = "cvr.passed"
    EventCVREscalated        = "cvr.escalated"
    EventCVRUnknown          = "cvr.unknown"
)
```

这些事件通过 SSE 暴露给 inspector，支持 CVR 执行过程的实时监控。

### 6.5 `internal/runtrace/runtrace.go` — StepRecord 扩展

```go
type StepRecord struct {
    // ... 现有字段 ...

    // CVR 新增字段（omitempty，向后兼容）
    VerifyStrategy  string `json:"verify_strategy,omitempty"`  // 策略名称
    VerifyOutcome   string `json:"verify_outcome,omitempty"`   // passed/failed/timeout/...
    RecoverAction   string `json:"recover_action,omitempty"`   // rollback/escalate/...
    RolledBack      bool   `json:"rolled_back,omitempty"`      // 是否触发了回滚
    ManifestID      string `json:"manifest_id,omitempty"`      // CheckpointManifest.ManifestID
}
```

### 6.6 `internal/primitive/macro.go` — 重构为 CVRCoordinator 的薄封装

`macro.safe_edit` 当前是 CVR 的手动实现。待 CVRCoordinator 完成后，
可重构为：

```go
// 简化版 macro.safe_edit（重构后）
func (m *MacroSafeEdit) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
    req := cvr.CVRRequest{
        Primitive: "fs.write",
        Params:    buildFSWriteParams(p),
        VerifyStrategy: &cvr.TestSuiteStrategy{
            Command:  p.TestCommand,
            MinTests: 1,
        },
        CheckpointPolicy: &cvr.CheckpointPolicy{Mode: cvr.CheckpointModeAlways},
        CheckpointLabel:  p.CheckpointLabel,
        MaxRetries:       0, // safe_edit 不重试，失败即回滚
    }
    result, err := m.coordinator.Execute(ctx, req)
    // 将 CVRResult 转换为现有 macro.safe_edit 的输出格式
    return convertToMacroResult(result), err
}
```

---

## 7. 包位置建议

```
internal/
  cvr/                           ← 新建包（本文档定义的所有类型）
    coordinator.go               ← CVRCoordinator 接口 + cvrCoordinator 实现
    manifest.go                  ← CheckpointManifest + CheckpointManifestStore
    strategy.go                  ← VerifyStrategy 接口 + 5 个具体实现
    recovery.go                  ← RecoveryDecisionTree + RecoverAction + RecoveryContext
    events.go                    ← CVR 事件常量
    coordinator_test.go          ← 单元测试（mock executor）
```

`internal/cvr` 的依赖关系：
- 依赖：`internal/primitive`（Schema、Result、RiskLevel）、`internal/eventing`
- 被依赖：`internal/orchestrator`（Engine 使用 CVRCoordinator）、`internal/runtime`（构建并注入 CVRCoordinator）
- **不依赖**：`internal/sandbox`、`internal/control`（通过接口解耦）

---

*文档结束。此设计草案供架构评审使用，不应直接生成代码。*
