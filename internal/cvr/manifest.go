package cvr

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// CVR Layer 语义约定：
// Layer A（前置 checkpoint 插入）失败时，必须短路跳过 Layer B 验证，
// 直接携带 LayerAErr 上下文进入 RecoveryDecisionTree。
// 不允许 Layer A 失败后继续执行原语。
// 执行顺序：Layer A → (短路或继续) → 原语执行 → Layer B → RecoveryDecisionTree

type LayerAErr struct{ Cause error }

func (e *LayerAErr) Error() string { return "layer_a_failed: " + e.Cause.Error() }

func (e *LayerAErr) Unwrap() error { return e.Cause }

const MaxCVRDepth = 5 // AIJudgeStrategy 递归调用最大深度，超出返回 ErrCVRDepthExceeded

var ErrCVRDepthExceeded = errors.New("cvr_depth_exceeded: max recursion depth reached, no further recovery attempted")

// ErrCVRDepthExceeded 触发时，CVRCoordinator 必须：
// 1. 不静默失败
// 2. 将 cvr_depth_exceeded=true 写入 ExecutionTrace
// 3. 直接上报给调用方，不再尝试任何恢复动作

// IntentCategory 表示一次原语调用的语义意图分类。
type IntentCategory string

const (
	IntentMutation     IntentCategory = "mutation"     // 写操作，改变状态
	IntentQuery        IntentCategory = "query"        // 只读，不改变状态
	IntentVerification IntentCategory = "verification" // 验证性操作
	IntentRollback     IntentCategory = "rollback"     // 恢复/回滚操作
)

// RiskLevel 表示一次原语调用的操作风险等级。
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// PrimitiveIntent 是 CVR 策略使用的结构化运行时意图快照。
type PrimitiveIntent struct {
	Category       IntentCategory `json:"category"`
	Reversible     bool           `json:"reversible"`
	RiskLevel      RiskLevel      `json:"risk_level"`
	AffectedScopes []string       `json:"affected_scopes,omitempty"`
}

// CheckpointTrigger records why a checkpoint was created.
type CheckpointTrigger string

const (
	TriggerManual        CheckpointTrigger = "manual"
	TriggerIntentPolicy  CheckpointTrigger = "intent_policy"   // 由 PrimitiveIntent 自动触发
	TriggerStrategyForce CheckpointTrigger = "strategy_forced" // 由显式 VerifyStrategy 触发
)

type CheckpointReason string

const (
	CheckpointReasonPreEdit      CheckpointReason = "pre_edit"
	CheckpointReasonPreRefactor  CheckpointReason = "pre_refactor"
	CheckpointReasonPreExec      CheckpointReason = "pre_exec"
	CheckpointReasonPreRestore   CheckpointReason = "pre_restore"
	CheckpointReasonManual       CheckpointReason = "manual"
	CheckpointReasonTaskBoundary CheckpointReason = "task_boundary"
)

type CallFrame struct {
	PrimitiveID string `json:"primitive_id"` // 调用该原语的 ID
	Depth       int    `json:"depth"`        // 当前递归深度（0 为顶层）
	CalledAtMs  int64  `json:"called_at_ms"` // Unix 毫秒时间戳
}

type EffectEntry struct {
	Kind            string   `json:"kind"`                       // "file_write"|"file_delete"|"shell_exec"
	Target          string   `json:"target"`                     // 受影响资源路径或标识
	AffectedSymbols []string `json:"affected_symbols,omitempty"` // Go 符号列表（M2 填充）
	ReversibleBy    string   `json:"reversible_by,omitempty"`    // 可通过哪个 primitive 逆转
}

type AppStateSnapshot struct {
	AppID        string          `json:"app_id"`         // 应用标识
	StateKey     string          `json:"state_key"`      // 状态键名
	StateJSON    json.RawMessage `json:"state_json"`     // 应用自定义状态快照
	CapturedAtMs int64           `json:"captured_at_ms"` // 采集时间的 Unix 毫秒时间戳
}

// CheckpointManifest 存储与 checkpoint 关联的语义元数据。
type CheckpointManifest struct {
	ID                     string             `json:"id"`
	CheckpointID           string             `json:"checkpoint_id"` // state.checkpoint 返回的 git commit hash
	SandboxID              string             `json:"sandbox_id"`
	PrimitiveID            string             `json:"primitive_id"`
	Intent                 PrimitiveIntent    `json:"intent"`
	Trigger                CheckpointTrigger  `json:"trigger"`
	CreatedAt              time.Time          `json:"created_at"`
	StateRef               string             `json:"state_ref"`                           // 指向实际 snapshot 的引用
	CommitHash             string             `json:"commit_hash,omitempty"`               // Git commit（可选）
	Label                  string             `json:"label,omitempty"`                     // 人工标注描述
	TriggerPrimitive       string             `json:"trigger_primitive,omitempty"`         // 触发 checkpoint 的原语 ID
	TriggerReason          CheckpointReason   `json:"trigger_reason"`                      // 触发 checkpoint 的原因
	TaskID                 string             `json:"task_id,omitempty"`                   // 关联任务 ID
	TraceID                string             `json:"trace_id,omitempty"`                  // 关联追踪 ID
	StepID                 string             `json:"step_id,omitempty"`                   // 关联步骤 ID
	Attempt                int                `json:"attempt"`                             // 当前重试次数（0 起）
	CallStack              []CallFrame        `json:"call_stack,omitempty"`                // 调用栈快照
	EffectLog              []EffectEntry      `json:"effect_log,omitempty"`                // 副作用日志
	AppStates              []AppStateSnapshot `json:"app_states,omitempty"`                // 应用状态快照列表
	WorkspaceRoot          string             `json:"workspace_root"`                      // 工作区根目录
	FilesModifiedSincePrev []string           `json:"files_modified_since_prev,omitempty"` // 相对上一个 checkpoint 修改的文件列表
	PrevCheckpointID       string             `json:"prev_checkpoint_id,omitempty"`        // 链表前驱
	Corrupted              bool               `json:"corrupted"`                           // 是否已损坏
	CorruptReason          string             `json:"corrupt_reason,omitempty"`            // 损坏原因
}

type CheckpointManifestStore interface {
	SaveManifest(ctx context.Context, m CheckpointManifest) error
	GetManifest(ctx context.Context, checkpointID string) (*CheckpointManifest, error)
	// GetManifestChain 从 checkpointID 向前追溯，返回完整链（含 checkpointID 本身），
	// 按时间倒序排列，最多返回 maxDepth 条（0 表示不限）
	GetManifestChain(ctx context.Context, checkpointID string, maxDepth int) ([]CheckpointManifest, error)
	MarkCorrupted(ctx context.Context, checkpointID string, reason string) error
}

type VerifyOutcome string

const (
	VerifyOutcomePassed  VerifyOutcome = "passed"
	VerifyOutcomeFailed  VerifyOutcome = "failed"
	VerifyOutcomeSkipped VerifyOutcome = "skipped"
	VerifyOutcomeTimeout VerifyOutcome = "timeout"
	VerifyOutcomeError   VerifyOutcome = "error"
)

type RecoverHint string

const (
	RecoverHintRollback RecoverHint = "rollback"
	RecoverHintRetry    RecoverHint = "retry"
	RecoverHintEscalate RecoverHint = "escalate"
	RecoverHintAbort    RecoverHint = "abort"
	RecoverHintRewrite  RecoverHint = "rewrite"
	RecoverHintSkip     RecoverHint = "skip"
)

type StrategyResult struct {
	Outcome     VerifyOutcome   `json:"outcome"`           // 验证结果枚举
	Message     string          `json:"message"`           // 面向调用方的结果说明
	Details     json.RawMessage `json:"details,omitempty"` // 结构化失败细节（测试输出等）
	RecoverHint RecoverHint     `json:"recover_hint"`      // 恢复建议
	DurationMs  int64           `json:"duration_ms"`       // 验证耗时（毫秒）
}

// StrategyExecutor 是 CVR 层对执行引擎的最小依赖接口，
// 由 internal/orchestrator/executor.go 实现，此处仅定义契约。
type StrategyExecutor interface {
	// Execute 执行指定原语，params 为 JSON 可序列化的参数对象
	Execute(ctx context.Context, method string, params any) (ExecuteResult, error)
}

// ExecuteResult 是原语执行的统一返回类型
type ExecuteResult struct {
	Data    map[string]json.RawMessage `json:"data"`              // 原语返回的结构化数据
	Success bool                       `json:"success"`           // 原语是否执行成功
	ErrMsg  string                     `json:"err_msg,omitempty"` // 失败时的错误消息
}

type VerifyStrategy interface {
	Name() string
	Description() string
	// Run 在原语执行完成后调用，exec 用于执行辅助验证操作（如跑测试），
	// result 是原语执行结果，manifest 是本次执行前创建的 checkpoint（可能为 nil）
	Run(ctx context.Context, exec StrategyExecutor, result ExecuteResult,
		manifest *CheckpointManifest) (StrategyResult, error)
}
