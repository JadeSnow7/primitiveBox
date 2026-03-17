# PrimitiveBox 原语类型体系设计

> 状态：架构草案（Iteration 4 设计阶段）
> 作者：架构设计会话，2026-03-16
> 依据：README.md、AGENTS.md、internal/primitive/ 全量阅读

---

## 0. 设计动机

现有 `Primitive` 接口（`Name / Category / Schema / Execute`）能支撑工具调用，
但还不是"AI 原生"的执行合约。AI 原生的原语必须携带四项能力：

| 能力 | 现状 | 目标 |
|------|------|------|
| 语义意图（intent） | 缺失，仅有 description | 每次调用显式声明 why |
| 影响范围（scope） | Schema 有 `scope` 字符串字段但未执行 | 机器可执行的边界声明 |
| 可逆性（reversible） | Schema 有 `checkpoint_required` 布尔值 | 结构化可逆性标注 |
| 验证与恢复（verify/recover） | 只有 `VerifierHint` 字符串提示 | 可执行的验证和恢复路径 |

本文档定义：
1. **基础类型体系**（共享 enum 和 struct）
2. **扩展接口**（`AIPrimitive`，在现有 `Primitive` 基础上组合）
3. **Layer 1 系统原语**评估与修订建议
4. **Layer 2 代码原语**（新设计）
5. **Layer 3 文档原语**（新设计）
6. **跨层约定**（checkpoint 语义、验证链、risk 矩阵）

---

## 1. 基础类型体系

```go
package primitive

import (
    "context"
    "encoding/json"
)

// --------------------------------------------------------------------------
// Scope — 声明一个原语可能影响的系统边界
// --------------------------------------------------------------------------

// ScopeBoundary 枚举原语影响的最大边界。
// 规则：调用方可信任此声明用于审计；执行时沙箱仍做实际隔离。
type ScopeBoundary string

const (
    // ScopeNone：纯读取，无任何持久化副作用。
    ScopeNone ScopeBoundary = "none"

    // ScopeFile：最多影响单个文件。
    ScopeFile ScopeBoundary = "file"

    // ScopeDirectory：影响一个目录树（可递归）。
    ScopeDirectory ScopeBoundary = "directory"

    // ScopeWorkspace：影响整个工作区（跨目录）。
    ScopeWorkspace ScopeBoundary = "workspace"

    // ScopeProcess：可能启动/终止进程，或产生进程级副作用。
    ScopeProcess ScopeBoundary = "process"

    // ScopeNetwork：可能产生网络 I/O。
    ScopeNetwork ScopeBoundary = "network"

    // ScopeSystem：可能影响宿主或容器系统级状态（挂载点、用户等）。
    ScopeSystem ScopeBoundary = "system"
)

// Scope 描述一次调用的具体影响范围。
// Boundary 是最大边界声明；Paths 和 Symbols 是更细粒度的提示，
// 用于 impact analysis 和 audit log，不做运行时强制（沙箱负责隔离）。
type Scope struct {
    Boundary ScopeBoundary `json:"boundary"`
    // Paths：受影响的文件/目录相对路径（相对于 workspace root）。
    // 可为空（表示 boundary 内任意位置）。
    Paths []string `json:"paths,omitempty"`
    // Symbols：受影响的代码符号（函数名、类型名等）。
    Symbols []string `json:"symbols,omitempty"`
}

// --------------------------------------------------------------------------
// RiskLevel — 执行前风险评估
// --------------------------------------------------------------------------

// RiskLevel 表示原语执行的风险等级。
// 用于 orchestrator 决策：是否需要在执行前先 checkpoint。
type RiskLevel string

const (
    // RiskNone：只读，没有任何副作用。
    RiskNone RiskLevel = "none"

    // RiskLow：有轻微副作用但高度可预测（如写单个已知文件）。
    RiskLow RiskLevel = "low"

    // RiskMedium：有持久化副作用，推荐执行前 checkpoint。
    RiskMedium RiskLevel = "medium"

    // RiskHigh：破坏性操作或影响范围难以完全预测，要求执行前 checkpoint。
    RiskHigh RiskLevel = "high"

    // RiskCritical：可能影响系统级状态或数据不可恢复；需要显式操作员确认。
    RiskCritical RiskLevel = "critical"
)

// --------------------------------------------------------------------------
// VerifyResult — 验证结果
// --------------------------------------------------------------------------

// VerifyOutcome 枚举验证的三种结论。
type VerifyOutcome string

const (
    VerifyPassed  VerifyOutcome = "passed"
    VerifyFailed  VerifyOutcome = "failed"
    VerifySkipped VerifyOutcome = "skipped" // 条件不满足，验证被跳过
)

// VerifyResult 是 Verify() 方法的返回值。
type VerifyResult struct {
    Outcome  VerifyOutcome `json:"outcome"`
    // Message：对 AI 友好的一句话摘要（passed 时简洁，failed 时说明原因）。
    Message  string        `json:"message"`
    // Details：机器可读的附加信息（测试计数、lint 错误列表等）。
    Details  any           `json:"details,omitempty"`
    // RecoverHint：如果失败，建议的恢复策略（供 orchestrator 决策）。
    RecoverHint RecoverStrategy `json:"recover_hint,omitempty"`
}

// --------------------------------------------------------------------------
// RecoverStrategy — 失败恢复建议
// --------------------------------------------------------------------------

// RecoverStrategy 告知 orchestrator 如何应对此原语的失败。
// 这是建议性的；orchestrator 的 RecoveryPolicy 有最终决定权。
type RecoverStrategy string

const (
    // RecoverNone：失败是终态，不建议重试或回滚。
    RecoverNone RecoverStrategy = "none"

    // RecoverRetry：可以原地重试（幂等操作）。
    RecoverRetry RecoverStrategy = "retry"

    // RecoverCheckpointRestore：应回滚到上一个 checkpoint。
    RecoverCheckpointRestore RecoverStrategy = "checkpoint_restore"

    // RecoverRewrite：建议 AI 重新生成参数后再试。
    RecoverRewrite RecoverStrategy = "rewrite"

    // RecoverEscalate：需要人工介入。
    RecoverEscalate RecoverStrategy = "escalate"
)

// --------------------------------------------------------------------------
// Checkpoint（引用类型，引用 state.checkpoint 产出的 ID）
// --------------------------------------------------------------------------

// CheckpointRef 是对 state.checkpoint 返回的 checkpoint_id 的引用。
// 不嵌入完整状态；Recover() 通过 checkpoint_id 触发 state.restore。
type CheckpointRef struct {
    ID    string `json:"id"`
    Label string `json:"label,omitempty"`
}

// --------------------------------------------------------------------------
// AIPrimitive — AI 原生原语扩展接口
// --------------------------------------------------------------------------

// AIPrimitive 在现有 Primitive 接口之上增加 AI 原生能力。
// 所有新设计的原语应实现此接口；现有原语可通过适配器逐步升级。
//
// 设计原则：
//   - AIPrimitive 组合 Primitive，不替换它，保持向后兼容。
//   - Verify 和 Recover 是一等方法，不是 Schema 里的提示字符串。
//   - EstimateRisk 在 Execute 前被 orchestrator 调用，用于决策是否需要 checkpoint。
type AIPrimitive interface {
    Primitive // 嵌入现有接口，保持 Name/Category/Schema/Execute

    // ---------- 元数据扩展 ----------

    // Intent 返回此原语的语义意图描述（为什么存在，不是怎么做）。
    // 例："安全地将文件内容替换为新内容，保留可回滚能力"
    // 用途：audit log、LLM tool description、inspector UI。
    Intent() string

    // DefaultScope 返回此原语在未提供 params 时的默认影响范围。
    // 具体调用的 scope 由 params 决定，此处返回的是"最坏情况"上界。
    DefaultScope() Scope

    // Reversible 声明此原语的副作用是否可通过 state.restore 完全撤销。
    // true：所有副作用都被 checkpoint 覆盖，restore 可还原。
    // false：存在无法通过 checkpoint 还原的外部副作用（如网络请求、数据库写入）。
    Reversible() bool

    // ---------- 执行前风险评估 ----------

    // EstimateRisk 根据具体 params 评估本次调用的风险等级。
    // orchestrator 在执行前调用此方法；返回 RiskHigh 或以上时，
    // orchestrator 应先执行 state.checkpoint。
    // params 可以为 nil（返回保守的默认风险估计）。
    EstimateRisk(params json.RawMessage) RiskLevel

    // ---------- AI 原生执行能力 ----------

    // Verify 验证 Execute 的结果是否符合预期。
    // 在 Execute 成功返回后，orchestrator 可调用此方法做后置验证。
    // result 是 Execute 的返回值；ctx 可携带 ExecContext。
    // 注意：Verify 本身不应有持久化副作用。
    Verify(ctx context.Context, result Result) (VerifyResult, error)

    // Recover 在失败后尝试恢复到给定的 checkpoint。
    // orchestrator 调用此方法时已决定需要回滚。
    // 实现通常调用 state.restore；某些原语可以实现更细粒度的恢复。
    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

---

## 2. Layer 1：系统原语评估与修订

### 2.1 评估矩阵

| 原语 | Intent 声明 | Scope 声明 | Reversible | Verify 能力 | EstimateRisk | AI 原生评分 |
|------|------------|-----------|-----------|------------|--------------|------------|
| `fs.read` | ❌ 缺失 | ❌ 未执行 | ✅ 只读 | ✅（隐式：文件存在则成功） | ✅ None | 3/5 |
| `fs.write` | ❌ 缺失 | ❌ 只有路径 | ⚠️ 依赖外部 checkpoint | ❌ 写后无验证 | ❌ 未声明 | 2/5 |
| `fs.list` | ❌ 缺失 | ✅ 目录明确 | ✅ 只读 | ✅ 隐式 | ✅ None | 3/5 |
| `fs.diff` | ✅ 描述清晰 | ✅ workspace | ✅ 只读 | ✅ 隐式 | ✅ None | 4/5 |
| `shell.exec` | ❌ **严重缺失** | ❌ **无边界** | ❌ | ❌ 只有 exit code | ❌ | **1/5** |
| `state.checkpoint` | ⚠️ label 是描述非意图 | ✅ workspace | N/A | ⚠️ 只检查 ID 是否生成 | ✅ Low | 3/5 |
| `state.restore` | ✅ 清晰 | ✅ workspace | ✅ 可再 checkpoint | ✅ 文件列表 | ⚠️ 未声明为 High | 3/5 |
| `state.list` | ✅ 只读 | ✅ workspace | ✅ 只读 | N/A | ✅ None | 4/5 |
| `verify.test` | ✅ 清晰 | ⚠️ workspace | ✅ 只读 | ⚠️ 只有 exit code，未解析失败详情 | ✅ Low | 3/5 |
| `code.search` | ✅ 只读 | ✅ workspace | ✅ 只读 | N/A | ✅ None | 4/5 |
| `code.symbols` | ✅ 只读 | ✅ file | ✅ 只读 | N/A | ✅ None | 4/5 |
| `macro.safe_edit` | ✅ 复合语义 | ✅ file | ✅ 含 checkpoint | ✅ 调用 verify.test | ✅ Medium | **5/5** |

### 2.2 `shell.exec` — 修订提案

`shell.exec` 是当前最不符合 AI 原生标准的原语：一个裸字符串命令，没有任何语义约束。

**修订后的 params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["command", "intent"],
  "properties": {
    "command": {
      "type": "string",
      "description": "要执行的 shell 命令（sh -c）"
    },
    "intent": {
      "type": "string",
      "description": "执行此命令的语义目的（必填）。例：'运行单元测试以验证代码修改'",
      "minLength": 10
    },
    "allowed_write_paths": {
      "type": "array",
      "items": { "type": "string" },
      "description": "命令可能写入的路径（相对于 workspace）。为空则视为只读意图。"
    },
    "expected_exit_codes": {
      "type": "array",
      "items": { "type": "integer" },
      "default": [0],
      "description": "视为成功的 exit code 列表"
    },
    "timeout_s": {
      "type": "integer",
      "description": "超时秒数，默认 30"
    },
    "env": {
      "type": "object",
      "additionalProperties": { "type": "string" }
    }
  }
}
```

**output schema（修订后新增字段）：**

```json
{
  "type": "object",
  "properties": {
    "stdout":       { "type": "string" },
    "stderr":       { "type": "string" },
    "exit_code":    { "type": "integer" },
    "duration_ms":  { "type": "integer" },
    "timed_out":    { "type": "boolean" },
    "succeeded":    {
      "type": "boolean",
      "description": "exit_code 是否在 expected_exit_codes 中"
    },
    "write_manifest": {
      "type": "array",
      "items": { "type": "string" },
      "description": "执行后检测到的实际写入文件列表（git status diff）"
    }
  }
}
```

**接口草案：**

```go
// ShellExecPrimitive 是 shell.exec 的 AIPrimitive 升级版。
// 新增：intent 必填、scope 通过 allowed_write_paths 声明、
// Verify 检查 write_manifest 是否超出声明范围。
type ShellExecPrimitive interface {
    AIPrimitive

    // Intent 固定返回类别描述；具体意图来自 params.intent。
    Intent() string // "在沙箱内执行一个受限 shell 命令"

    DefaultScope() Scope // ScopeProcess（最坏情况）

    Reversible() bool // false（外部副作用无法通过 checkpoint 完全覆盖）

    EstimateRisk(params json.RawMessage) RiskLevel
    // 规则：
    //   allowed_write_paths 为空 → RiskLow（只读意图）
    //   allowed_write_paths 非空 → RiskMedium
    //   command 包含 rm / drop / truncate 等关键字 → RiskHigh

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 验证逻辑：
    //   1. exit_code 在 expected_exit_codes 中 → passed
    //   2. 如果 allowed_write_paths 非空，检查 write_manifest ⊆ allowed_write_paths
    //      超出范围的写入 → failed，RecoverHint = checkpoint_restore
    //   3. 如果 timed_out → failed，RecoverHint = retry

    Recover(ctx context.Context, checkpoint CheckpointRef) error
    // 调用 state.restore(checkpoint.ID)
}
```

### 2.3 `fs.write` — 修订提案

**新增字段：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["path", "intent"],
  "properties": {
    "path":    { "type": "string" },
    "intent":  {
      "type": "string",
      "description": "修改此文件的语义原因。例：'修复 parseJSON 中的空指针解引用'",
      "minLength": 10
    },
    "content": { "type": "string" },
    "mode": {
      "type": "string",
      "enum": ["overwrite", "search_replace"],
      "default": "search_replace",
      "description": "默认使用 search_replace（更安全），仅在创建新文件时用 overwrite"
    },
    "search":      { "type": "string" },
    "replace":     { "type": "string" },
    "create_dirs": { "type": "boolean", "default": false },
    "verify_syntax": {
      "type": "boolean",
      "default": false,
      "description": "写入后是否尝试语法检查（如 go vet / python -m py_compile）"
    }
  }
}
```

**接口草案：**

```go
type FSWritePrimitive interface {
    AIPrimitive

    Intent() string   // "将文件内容替换为新内容，保留可回滚路径"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（通过 state.checkpoint 覆盖）

    EstimateRisk(params json.RawMessage) RiskLevel
    // mode=search_replace → RiskLow（改动可预测）
    // mode=overwrite      → RiskMedium（全量覆盖）

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 验证逻辑：
    //   1. bytes_written > 0（文件确实被写入）
    //   2. 如果 verify_syntax=true，调用语言对应的语法检查器
    //   3. diff 非空（search_replace 模式下，确实有内容变化）

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

### 2.4 `state.checkpoint` / `state.restore` — 修订提案

`state.checkpoint` 是最接近 AI 原生的原语。主要补充：

- `intent` 字段应为建议必填（用于 inspector 和 replay）
- `state.restore` 的 RiskLevel 应声明为 `RiskHigh`（会丢弃未检查点的变更）

**`state.checkpoint` params schema（修订）：**

```json
{
  "type": "object",
  "properties": {
    "label": {
      "type": "string",
      "description": "检查点标签（建议必填，供 inspector 使用）"
    },
    "intent": {
      "type": "string",
      "description": "创建此检查点的原因。例：'执行高风险重构前的安全点'"
    },
    "scope_hint": {
      "type": "string",
      "enum": ["pre_edit", "pre_refactor", "pre_test", "pre_deploy", "manual"],
      "description": "检查点的用途分类，用于 inspector 分组展示"
    }
  }
}
```

**`state.restore` params schema（修订）：**

```json
{
  "type": "object",
  "required": ["checkpoint_id", "intent"],
  "properties": {
    "checkpoint_id": { "type": "string" },
    "intent": {
      "type": "string",
      "description": "为什么需要回滚到此检查点",
      "minLength": 10
    },
    "dry_run": {
      "type": "boolean",
      "default": false,
      "description": "仅列出会被还原的文件，不实际执行"
    }
  }
}
```

### 2.5 `verify.test` — 修订提案

**问题：** 只检查 exit code，不解析测试框架输出，无法区分"0 个测试运行"和"所有测试通过"。

**修订后 params schema：**

```json
{
  "type": "object",
  "properties": {
    "command":    { "type": "string", "default": "pytest" },
    "test_filter": {
      "type": "string",
      "description": "只运行匹配此 pattern 的测试（pytest -k / go test -run）"
    },
    "expected_min_tests": {
      "type": "integer",
      "default": 1,
      "description": "至少需要运行的测试数量（防止误判空测试集为通过）"
    },
    "timeout_s":  { "type": "integer" },
    "framework": {
      "type": "string",
      "enum": ["pytest", "go_test", "jest", "cargo_test", "auto"],
      "default": "auto",
      "description": "用于解析输出的测试框架类型"
    }
  }
}
```

**修订后 output schema：**

```json
{
  "type": "object",
  "properties": {
    "passed":        { "type": "boolean" },
    "total":         { "type": "integer" },
    "passed_count":  { "type": "integer" },
    "failed_count":  { "type": "integer" },
    "error_count":   { "type": "integer" },
    "skipped_count": { "type": "integer" },
    "failures": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "test_name":  { "type": "string" },
          "file":       { "type": "string" },
          "line":       { "type": "integer" },
          "message":    { "type": "string" }
        }
      },
      "description": "失败的测试列表（用于 AI 精准定位问题）"
    },
    "output":    { "type": "string" },
    "summary":   { "type": "string" }
  }
}
```

---

## 3. Layer 2：代码原语（Code Primitives）

代码原语在**语义层**操作代码，而非文本层。核心原则：AI 操作的最小单位是符号（函数、类型、模块），不是行号。

### 3.1 `code.read_function`

**意图：** 按符号名读取函数定义，不依赖行号。

```go
type CodeReadFunctionPrimitive interface {
    AIPrimitive

    Intent() string   // "按符号名精确读取函数定义，不依赖行号"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（只读）
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 验证：symbol 字段非空 → passed；未找到符号 → 返回 failed + 建议备选
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["symbol"],
  "properties": {
    "symbol": {
      "type": "string",
      "description": "函数/方法名。方法可用 'ReceiverType.MethodName' 格式"
    },
    "file": {
      "type": "string",
      "description": "可选。限定搜索范围到指定文件（提高精度）"
    },
    "language": {
      "type": "string",
      "enum": ["go", "python", "typescript", "javascript", "rust", "auto"],
      "default": "auto"
    },
    "include_comments": {
      "type": "boolean",
      "default": true
    },
    "include_tests": {
      "type": "boolean",
      "default": false,
      "description": "同时返回此函数的测试用例"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "symbol":     { "type": "string" },
    "file":       { "type": "string" },
    "start_line": { "type": "integer" },
    "end_line":   { "type": "integer" },
    "signature":  { "type": "string", "description": "仅函数签名行" },
    "body":       { "type": "string", "description": "完整函数体（含签名）" },
    "language":   { "type": "string" },
    "imports_used": {
      "type": "array",
      "items": { "type": "string" },
      "description": "此函数用到的 import 列表（用于 write_function 时保留依赖）"
    },
    "tests": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "symbol": { "type": "string" },
          "file":   { "type": "string" }
        }
      }
    }
  }
}
```

### 3.2 `code.write_function`

**意图：** 按符号名替换或插入函数定义，不依赖行号，不破坏文件其他部分。

> 设计关键：AI 给出新函数体，原语负责定位旧定义位置、执行替换、保持缩进和文件完整性。

```go
type CodeWriteFunctionPrimitive interface {
    AIPrimitive

    Intent() string  // "按符号名替换函数定义，保持文件其余部分不变"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（通过 checkpoint 可还原）
    EstimateRisk(params json.RawMessage) RiskLevel
    // create=false（修改已有函数）→ RiskMedium
    // create=true（插入新函数）→ RiskLow

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 1. 写入后重新 read_function，检查 symbol 确实存在
    // 2. 如果 verify_syntax=true，执行语法检查
    // 3. 如果 run_tests=true，执行相关测试

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["symbol", "file", "content", "intent"],
  "properties": {
    "symbol": {
      "type": "string",
      "description": "目标函数/方法名"
    },
    "file": {
      "type": "string",
      "description": "目标文件（相对于 workspace）"
    },
    "content": {
      "type": "string",
      "description": "完整的新函数定义（含签名、注释、函数体）"
    },
    "intent": {
      "type": "string",
      "description": "修改原因。例：'修复 handleRequest 中对 nil context 的处理'",
      "minLength": 10
    },
    "create_if_missing": {
      "type": "boolean",
      "default": false,
      "description": "如果符号不存在，是否插入到文件末尾"
    },
    "insert_after": {
      "type": "string",
      "description": "当 create_if_missing=true 时，插入到此符号之后（可选）"
    },
    "verify_syntax": {
      "type": "boolean",
      "default": true,
      "description": "写入后执行语法检查"
    },
    "run_tests": {
      "type": "array",
      "items": { "type": "string" },
      "description": "写入后运行的测试用例 pattern（可为空）"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "symbol":    { "type": "string" },
    "file":      { "type": "string" },
    "action":    { "type": "string", "enum": ["replaced", "created"] },
    "old_start_line": { "type": "integer" },
    "old_end_line":   { "type": "integer" },
    "new_start_line": { "type": "integer" },
    "new_end_line":   { "type": "integer" },
    "diff":           { "type": "string" },
    "syntax_ok":      { "type": "boolean" },
    "tests_passed":   { "type": "boolean" }
  }
}
```

### 3.3 `code.read_module`

**意图：** 获取文件的模块级结构（不含函数体），用于 AI 快速理解文件轮廓。

```go
type CodeReadModulePrimitive interface {
    AIPrimitive

    Intent() string   // "返回文件的顶层结构：导入、类型、函数签名（不含函数体）"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "type": "object",
  "required": ["file"],
  "properties": {
    "file":     { "type": "string" },
    "language": { "type": "string", "enum": ["go","python","typescript","javascript","rust","auto"], "default": "auto" },
    "include_private": {
      "type": "boolean",
      "default": true,
      "description": "是否包含非导出（小写/下划线前缀）符号"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "file":     { "type": "string" },
    "language": { "type": "string" },
    "package":  { "type": "string" },
    "imports": {
      "type": "array",
      "items": { "type": "string" }
    },
    "types": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "kind": { "type": "string", "enum": ["struct","interface","enum","alias","class"] },
          "line": { "type": "integer" },
          "exported": { "type": "boolean" }
        }
      }
    },
    "functions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name":      { "type": "string" },
          "signature": { "type": "string" },
          "line":      { "type": "integer" },
          "exported":  { "type": "boolean" },
          "receiver":  { "type": "string" }
        }
      }
    },
    "constants": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name":  { "type": "string" },
          "value": { "type": "string" },
          "line":  { "type": "integer" }
        }
      }
    }
  }
}
```

### 3.4 `code.impact_analysis`

**意图：** 在变更符号之前，评估影响范围（依赖图感知）。这是 AI 规划变更时的核心决策原语。

```go
type CodeImpactAnalysisPrimitive interface {
    AIPrimitive

    Intent() string
    // "分析修改某符号会影响哪些调用方、测试、导出接口——执行前风险评估"
    DefaultScope() Scope  // { Boundary: ScopeWorkspace }
    Reversible() bool // true（只读分析）
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["symbol", "file"],
  "properties": {
    "symbol": { "type": "string" },
    "file":   { "type": "string" },
    "change_type": {
      "type": "string",
      "enum": ["signature_change", "rename", "removal", "behavior_change", "any"],
      "default": "any",
      "description": "预期的变更类型（影响分析的保守程度）"
    },
    "max_depth": {
      "type": "integer",
      "default": 3,
      "description": "依赖图向上追溯的最大层数"
    },
    "include_tests": {
      "type": "boolean",
      "default": true
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "symbol":      { "type": "string" },
    "file":        { "type": "string" },
    "change_type": { "type": "string" },
    "callers": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "symbol":   { "type": "string" },
          "file":     { "type": "string" },
          "line":     { "type": "integer" },
          "depth":    { "type": "integer", "description": "依赖图层数（1=直接调用方）" },
          "exported": { "type": "boolean" }
        }
      }
    },
    "test_coverage": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "test_symbol": { "type": "string" },
          "test_file":   { "type": "string" }
        }
      }
    },
    "affected_files": {
      "type": "array",
      "items": { "type": "string" }
    },
    "risk_summary": {
      "type": "object",
      "properties": {
        "caller_count":          { "type": "integer" },
        "exported_caller_count": { "type": "integer" },
        "test_count":            { "type": "integer" },
        "breaks_public_api":     { "type": "boolean" },
        "estimated_risk":        { "type": "string", "enum": ["none","low","medium","high"] }
      }
    }
  }
}
```

### 3.5 `code.rename`

**意图：** 重命名符号（跨文件），保证引用一致性。

```go
type CodeRenamePrimitive interface {
    AIPrimitive

    Intent() string  // "重命名符号并同步所有引用，保持代码语义不变"
    DefaultScope() Scope  // { Boundary: ScopeWorkspace }
    Reversible() bool // true（通过 checkpoint + 反向 rename 可还原）
    EstimateRisk(params json.RawMessage) RiskLevel
    // exported symbol → RiskHigh（影响 public API）
    // unexported      → RiskMedium

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 1. 旧名称不再出现在任何源文件中（排除注释和字符串）
    // 2. 新名称在预期文件中存在
    // 3. 语法检查通过

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["symbol", "new_name", "file", "intent"],
  "properties": {
    "symbol": {
      "type": "string",
      "description": "要重命名的符号（当前名称）"
    },
    "new_name": {
      "type": "string",
      "description": "新名称"
    },
    "file": {
      "type": "string",
      "description": "符号定义所在文件（用于消歧义）"
    },
    "symbol_kind": {
      "type": "string",
      "enum": ["function", "method", "type", "variable", "constant", "package", "field"],
      "description": "符号类型（提高精度）"
    },
    "intent": {
      "type": "string",
      "description": "重命名原因",
      "minLength": 10
    },
    "dry_run": {
      "type": "boolean",
      "default": false,
      "description": "只预览受影响的位置，不实际修改"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "old_name":        { "type": "string" },
    "new_name":        { "type": "string" },
    "files_modified":  { "type": "array", "items": { "type": "string" } },
    "replacements":    { "type": "integer" },
    "diff":            { "type": "string" },
    "dry_run":         { "type": "boolean" }
  }
}
```

### 3.6 `code.extract_function`

**意图：** 将代码块提取为独立函数（Extract Function 重构）。

```go
type CodeExtractFunctionPrimitive interface {
    AIPrimitive

    Intent() string  // "将代码块提取为独立函数，减少重复并提高可测试性"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel // RiskMedium

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 1. 新函数符号可通过 read_function 找到
    // 2. 原调用点已替换为函数调用
    // 3. 语法检查通过

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file", "start_line", "end_line", "new_function_name", "intent"],
  "properties": {
    "file":              { "type": "string" },
    "start_line":        { "type": "integer" },
    "end_line":          { "type": "integer" },
    "new_function_name": { "type": "string" },
    "intent": {
      "type": "string",
      "description": "提取原因。例：'将验证逻辑提取以便单独测试'",
      "minLength": 10
    },
    "parameters": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "type": { "type": "string" }
        }
      },
      "description": "显式声明参数（为空则自动推断）"
    },
    "return_types": {
      "type": "array",
      "items": { "type": "string" },
      "description": "显式声明返回类型（为空则自动推断）"
    },
    "insert_location": {
      "type": "string",
      "enum": ["before_caller", "after_caller", "end_of_file"],
      "default": "before_caller"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "new_function_name": { "type": "string" },
    "new_function_line": { "type": "integer" },
    "call_site_line":    { "type": "integer" },
    "diff":              { "type": "string" }
  }
}
```

### 3.7 `code.inline_function`

**意图：** 将函数内联到所有（或指定）调用点（Inline Function 重构）。

```go
type CodeInlineFunctionPrimitive interface {
    AIPrimitive

    Intent() string  // "消除不必要的函数间接层，将函数体展开到调用点"
    DefaultScope() Scope  // { Boundary: ScopeWorkspace }
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel
    // all call sites → RiskHigh（影响范围难以预测）
    // specific call sites → RiskMedium

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["symbol", "file", "intent"],
  "properties": {
    "symbol":      { "type": "string" },
    "file":        { "type": "string", "description": "符号定义文件" },
    "intent": {
      "type": "string",
      "description": "内联原因。例：'formatDate 仅被调用一次，不需要独立函数'",
      "minLength": 10
    },
    "call_sites": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "file": { "type": "string" },
          "line": { "type": "integer" }
        }
      },
      "description": "限定内联的调用点（为空则内联所有调用点）"
    },
    "remove_original": {
      "type": "boolean",
      "default": true,
      "description": "所有调用点内联后，是否删除原函数定义"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "symbol":          { "type": "string" },
    "inlined_count":   { "type": "integer" },
    "original_removed": { "type": "boolean" },
    "files_modified":  { "type": "array", "items": { "type": "string" } },
    "diff":            { "type": "string" }
  }
}
```

### 3.8 与现有 `verify.test` 的关系

Layer 2 代码原语不替换 `verify.test`，而是形成验证层级：

```
code.write_function
  └─ 内部 Verify: verify_syntax（语法检查）
       └─ 调用方可链式触发: verify.test（测试验证）
            └─ 如失败: state.restore（checkpoint 回滚）
```

`verify.test` 定位为**工作区级别的集成验证**；代码原语的 `Verify` 方法是**单符号级别的局部验证**。两者在 orchestrator 的任务链中分层组合。

---

## 4. Layer 3：文档原语（Document Primitives）

文档原语在**结构层**操作有约束的文档（Markdown、YAML、JSON Schema 等），而非字节级文本。核心保证：操作后文档的结构完整性不被破坏。

### 4.1 `doc.read_section`

**意图：** 按标题路径读取文档的特定章节，不依赖行号。

```go
type DocReadSectionPrimitive interface {
    AIPrimitive

    Intent() string   // "按标题路径精确读取文档章节，不依赖行号"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（只读）
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file", "section_path"],
  "properties": {
    "file": { "type": "string" },
    "section_path": {
      "type": "array",
      "items": { "type": "string" },
      "description": "标题层级路径。例：['Architecture', 'Router Layer']"
    },
    "include_subsections": {
      "type": "boolean",
      "default": true,
      "description": "是否包含子章节内容"
    },
    "format": {
      "type": "string",
      "enum": ["markdown", "plain_text"],
      "default": "markdown"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "section_path":  { "type": "array", "items": { "type": "string" } },
    "heading":       { "type": "string" },
    "heading_level": { "type": "integer" },
    "content":       { "type": "string" },
    "start_line":    { "type": "integer" },
    "end_line":      { "type": "integer" },
    "subsections": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "heading": { "type": "string" },
          "level":   { "type": "integer" }
        }
      }
    }
  }
}
```

### 4.2 `doc.append_section`

**意图：** 在指定父章节下追加新章节，保证标题层级一致性。

```go
type DocAppendSectionPrimitive interface {
    AIPrimitive

    Intent() string  // "在指定父章节下插入新章节，保持标题层级结构"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（通过 checkpoint 可还原）
    EstimateRisk(params json.RawMessage) RiskLevel // RiskLow

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 1. 新章节可通过 doc.read_section 找到
    // 2. 调用 doc.verify_structure 检查层级未破坏

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file", "heading", "content", "intent"],
  "properties": {
    "file": { "type": "string" },
    "parent_section": {
      "type": "array",
      "items": { "type": "string" },
      "description": "父章节路径。为空则插入到文档顶层。"
    },
    "heading": {
      "type": "string",
      "description": "新章节的标题文本（不含 # 符号）"
    },
    "content": {
      "type": "string",
      "description": "新章节内容（Markdown）"
    },
    "intent": {
      "type": "string",
      "description": "添加此章节的原因",
      "minLength": 10
    },
    "insert_position": {
      "type": "string",
      "enum": ["after_last_child", "before_first_child", "after_sibling"],
      "default": "after_last_child"
    },
    "after_sibling": {
      "type": "string",
      "description": "当 insert_position=after_sibling 时，指定前一个兄弟章节标题"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "heading":       { "type": "string" },
    "inserted_line": { "type": "integer" },
    "diff":          { "type": "string" }
  }
}
```

### 4.3 `doc.update_section`

**意图：** 替换指定章节的内容，可选保留其子章节。

```go
type DocUpdateSectionPrimitive interface {
    AIPrimitive

    Intent() string  // "替换文档章节内容，保持标题结构和子章节不变（可选）"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel // RiskMedium

    Verify(ctx context.Context, result Result) (VerifyResult, error)
    // 1. 章节内容已更新（通过 read_section 验证）
    // 2. 如果 preserve_subsections=true，子章节数量未减少
    // 3. doc.verify_structure 通过

    Recover(ctx context.Context, checkpoint CheckpointRef) error
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file", "section_path", "content", "intent"],
  "properties": {
    "file": { "type": "string" },
    "section_path": {
      "type": "array",
      "items": { "type": "string" }
    },
    "content": {
      "type": "string",
      "description": "新的章节内容（不含章节标题行）"
    },
    "intent": {
      "type": "string",
      "description": "更新原因",
      "minLength": 10
    },
    "preserve_subsections": {
      "type": "boolean",
      "default": true,
      "description": "是否保留原章节的所有子章节（只替换当前章节的直接内容）"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "section_path":       { "type": "array", "items": { "type": "string" } },
    "old_content_lines":  { "type": "integer" },
    "new_content_lines":  { "type": "integer" },
    "subsections_preserved": { "type": "integer" },
    "diff":               { "type": "string" }
  }
}
```

### 4.4 `doc.verify_structure`

**意图：** 验证文档的结构完整性（章节层级、必需章节、引用锚点）。

```go
type DocVerifyStructurePrimitive interface {
    AIPrimitive

    Intent() string  // "验证文档结构：标题层级、必需章节存在性、内部锚点一致性"
    DefaultScope() Scope  // { Boundary: ScopeFile }
    Reversible() bool // true（只读）
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error) // self-validating
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file"],
  "properties": {
    "file": { "type": "string" },
    "required_sections": {
      "type": "array",
      "items": { "type": "string" },
      "description": "必须存在的顶层章节标题列表"
    },
    "check_heading_hierarchy": {
      "type": "boolean",
      "default": true,
      "description": "检查标题层级不跳级（如 H2 直接到 H4）"
    },
    "check_anchor_references": {
      "type": "boolean",
      "default": true,
      "description": "检查文档内部的 [text](#anchor) 引用能找到对应章节"
    },
    "max_heading_depth": {
      "type": "integer",
      "default": 4,
      "description": "允许的最大标题层级深度"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "passed": { "type": "boolean" },
    "violations": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "kind": {
            "type": "string",
            "enum": [
              "missing_required_section",
              "heading_level_skip",
              "broken_anchor_reference",
              "max_depth_exceeded"
            ]
          },
          "line":    { "type": "integer" },
          "message": { "type": "string" }
        }
      }
    },
    "section_count":  { "type": "integer" },
    "max_depth_seen": { "type": "integer" }
  }
}
```

### 4.5 `doc.verify_references`

**意图：** 检查文档中的所有引用（文件路径、内部锚点）是否可解析。

```go
type DocVerifyReferencesPrimitive interface {
    AIPrimitive

    Intent() string  // "验证文档引用：文件路径、内部锚点是否都能解析"
    DefaultScope() Scope  // { Boundary: ScopeWorkspace } // 需要读取被引用的文件
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["file"],
  "properties": {
    "file": { "type": "string" },
    "check_file_refs": {
      "type": "boolean",
      "default": true,
      "description": "验证 [text](./relative/path) 形式的文件路径引用"
    },
    "check_internal_anchors": {
      "type": "boolean",
      "default": true,
      "description": "验证 [text](#anchor) 形式的同文档内部锚点"
    },
    "check_cross_doc_anchors": {
      "type": "boolean",
      "default": true,
      "description": "验证 [text](other.md#anchor) 形式的跨文档引用"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "passed": { "type": "boolean" },
    "broken_refs": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "kind": {
            "type": "string",
            "enum": ["broken_file_ref", "broken_internal_anchor", "broken_cross_doc_anchor"]
          },
          "ref":     { "type": "string" },
          "line":    { "type": "integer" },
          "message": { "type": "string" }
        }
      }
    },
    "total_refs":  { "type": "integer" },
    "broken_count": { "type": "integer" }
  }
}
```

### 4.6 `doc.verify_consistency`

**意图：** 检查多个文档之间的内容一致性（版本提及、API 名称等）。

```go
type DocVerifyConsistencyPrimitive interface {
    AIPrimitive

    Intent() string
    // "跨文档比对：版本号、API 名称、CLI 参数等提及是否保持一致"
    DefaultScope() Scope  // { Boundary: ScopeWorkspace }
    Reversible() bool // true
    EstimateRisk(params json.RawMessage) RiskLevel // RiskNone
    Verify(ctx context.Context, result Result) (VerifyResult, error)
    Recover(ctx context.Context, checkpoint CheckpointRef) error // no-op
}
```

**params schema：**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["files"],
  "properties": {
    "files": {
      "type": "array",
      "items": { "type": "string" },
      "description": "要检查一致性的文档路径列表"
    },
    "check_version_mentions": {
      "type": "boolean",
      "default": true,
      "description": "提取并比较 version/v[0-9] 形式的版本提及"
    },
    "check_cli_commands": {
      "type": "boolean",
      "default": true,
      "description": "提取并比较 `pb xxx` 形式的 CLI 命令提及"
    },
    "check_api_routes": {
      "type": "boolean",
      "default": true,
      "description": "提取并比较 /api/v*/... 形式的 HTTP 路由提及"
    },
    "canonical_file": {
      "type": "string",
      "description": "以此文件为真相来源；其他文件与之比对（可选）"
    }
  }
}
```

**output schema：**

```json
{
  "type": "object",
  "properties": {
    "passed": { "type": "boolean" },
    "inconsistencies": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "kind": {
            "type": "string",
            "enum": ["version_mismatch", "cli_command_mismatch", "api_route_mismatch"]
          },
          "term":    { "type": "string", "description": "不一致的词条" },
          "occurrences": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "file":  { "type": "string" },
                "line":  { "type": "integer" },
                "value": { "type": "string" }
              }
            }
          }
        }
      }
    }
  }
}
```

---

## 5. 跨层约定

### 5.1 Checkpoint 语义链

Orchestrator 按以下规则决定是否在执行前插入 checkpoint：

```
EstimateRisk(params) → RiskLevel
  RiskNone    → 不需要 checkpoint
  RiskLow     → 不需要（但记录 intent 到 audit log）
  RiskMedium  → 建议 checkpoint（orchestrator 可配置为强制）
  RiskHigh    → 强制 checkpoint（必须在 Execute 前执行）
  RiskCritical → 强制 checkpoint + 等待操作员确认
```

checkpoint 与原语之间的关联通过 `CheckpointRef` 传递：

```
state.checkpoint(intent="执行 code.rename 前") → CheckpointRef{id, label}
  ↓
code.rename.Execute(params)
  ↓ 失败
code.rename.Recover(ctx, CheckpointRef{id}) → state.restore(id)
```

### 5.2 Verify 链（局部 → 全局）

每个原语的 `Verify` 是局部验证（该原语自身的正确性断言）。
全局验证由 orchestrator 在任务步骤完成后调用 `verify.test`。

```
code.write_function.Execute(params)
  → code.write_function.Verify(result)   // 局部：语法、符号存在性
    → verify.test(command="go test ./...")  // 全局：测试套件
      → 如果 failed: state.restore(pre_write_checkpoint)
```

### 5.3 风险矩阵速查

| 原语 | 默认风险 | 参数影响 |
|------|---------|---------|
| `fs.read` | None | — |
| `fs.write` (search_replace) | Low | overwrite → Medium |
| `fs.write` (overwrite) | Medium | — |
| `shell.exec` (无写路径) | Low | 有写路径 → Medium；含 rm → High |
| `state.checkpoint` | Low | — |
| `state.restore` | High | dry_run=true → None |
| `verify.test` | Low | — |
| `code.read_*` | None | — |
| `code.write_function` | Medium | create=true → Low |
| `code.rename` (unexported) | Medium | — |
| `code.rename` (exported) | High | — |
| `code.extract_function` | Medium | — |
| `code.inline_function` (all sites) | High | specific sites → Medium |
| `code.impact_analysis` | None | — |
| `doc.read_section` | None | — |
| `doc.append_section` | Low | — |
| `doc.update_section` | Medium | — |
| `doc.verify_*` | None | — |

### 5.4 注册规范

新原语实现 `AIPrimitive` 后，仍通过现有 `Registry` 注册，但需额外调用：

```go
// 注册时声明扩展元数据（向后兼容，不破坏现有 Primitive 接口）
registry.RegisterAI(primitive AIPrimitive)

// 降级适配：将现有 Primitive 包装为最小 AIPrimitive（用于过渡期）
registry.RegisterWithDefaults(primitive Primitive, riskLevel RiskLevel)
```

`Schema` 结构体应新增以下字段（向后兼容，omitempty）：

```go
type Schema struct {
    // ... 现有字段 ...

    // AI 原生扩展字段（新增，omitempty 保持向后兼容）
    Intent          string    `json:"intent,omitempty"`
    DefaultBoundary string    `json:"default_boundary,omitempty"` // ScopeBoundary 字符串
    DefaultRisk     string    `json:"default_risk,omitempty"`     // RiskLevel 字符串
    IsReversible    *bool     `json:"is_reversible,omitempty"`
    HasVerify       bool      `json:"has_verify,omitempty"`
    HasRecover      bool      `json:"has_recover,omitempty"`
}
```

---

## 6. 迁移路径

现有 `Primitive` 接口不变，`AIPrimitive` 以组合方式叠加能力。建议按以下顺序推进：

| 阶段 | 目标 | 涉及原语 |
|------|------|---------|
| P1（当前迭代）| 定义 `AIPrimitive` 接口和基础类型 | 仅接口定义，不改现有实现 |
| P2 | 为现有高风险原语添加 `intent` 字段（params）和 `EstimateRisk` | `shell.exec`、`fs.write`、`state.restore` |
| P3 | 实现 Layer 2 代码原语（read_function、write_function、impact_analysis） | 新原语 |
| P4 | 实现 `code.rename`、`code.extract_function` | 需要 LSP 或 AST 支持 |
| P5 | 实现 Layer 3 文档原语 | 新原语 |
| P6 | 全部现有原语升级为 `AIPrimitive` | 全量 |

> Layer 2/3 中部分原语（`code.rename`、`code.extract_function`、`code.inline_function`）
> 需要语言感知的 AST 操作；初期可以基于 `code.search` + `fs.write` 组合实现简化版，
> 后续通过适配器（语言 server / LSP 进程）提供高精度实现。

---

*文档结束。此设计草案供架构评审使用，不应直接生成代码。实现前需进行接口评审。*
