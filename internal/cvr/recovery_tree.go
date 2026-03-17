package cvr

type RecoveryAction string

const (
	RecoveryActionRetry    RecoveryAction = "retry"
	RecoveryActionRollback RecoveryAction = "rollback"
	RecoveryActionRewrite  RecoveryAction = "rewrite"
	RecoveryActionSkip     RecoveryAction = "skip"
	RecoveryActionEscalate RecoveryAction = "escalate"
	RecoveryActionAbort    RecoveryAction = "abort"
)

type FailureKind string

const (
	FailureKindExecError  FailureKind = "exec_error"
	FailureKindVerifyFail FailureKind = "verify_fail"
	FailureKindDuplicate  FailureKind = "duplicate"
	FailureKindTimeout    FailureKind = "timeout"
)

type RecoveryCtx struct {
	FailureKind    FailureKind
	Attempt        int
	Intent         PrimitiveIntent
	StrategyResult StrategyResult
	MaxRetries     int
}

type DecisionNode interface {
	Match(ctx RecoveryCtx) bool
	Action() RecoveryAction
}

type DecisionTree struct {
	nodes []DecisionNode
}

func (t *DecisionTree) Decide(ctx RecoveryCtx) RecoveryAction {
	if t == nil {
		return RecoveryActionAbort
	}
	for _, n := range t.nodes {
		if n.Match(ctx) {
			return n.Action()
		}
	}
	return RecoveryActionAbort
}

type IrreversibleMutationNode struct{}

func (n IrreversibleMutationNode) Match(ctx RecoveryCtx) bool {
	return !ctx.Intent.Reversible && ctx.StrategyResult.Outcome == VerifyOutcomeFailed
}

func (n IrreversibleMutationNode) Action() RecoveryAction { return RecoveryActionRollback }

type MaxAttemptsNode struct{}

func (n MaxAttemptsNode) Match(ctx RecoveryCtx) bool {
	return ctx.Attempt >= ctx.MaxRetries
}

func (n MaxAttemptsNode) Action() RecoveryAction { return RecoveryActionEscalate }

type DuplicateNode struct{}

func (n DuplicateNode) Match(ctx RecoveryCtx) bool {
	return ctx.FailureKind == FailureKindDuplicate
}

func (n DuplicateNode) Action() RecoveryAction { return RecoveryActionAbort }

type TimeoutNode struct{}

func (n TimeoutNode) Match(ctx RecoveryCtx) bool {
	return ctx.FailureKind == FailureKindTimeout
}

func (n TimeoutNode) Action() RecoveryAction { return RecoveryActionRetry }

type DefaultRetryNode struct{}

func (n DefaultRetryNode) Match(ctx RecoveryCtx) bool { return true }

func (n DefaultRetryNode) Action() RecoveryAction { return RecoveryActionRetry }

func NewDefaultDecisionTree() *DecisionTree {
	return &DecisionTree{
		nodes: []DecisionNode{
			IrreversibleMutationNode{},
			MaxAttemptsNode{},
			DuplicateNode{},
			TimeoutNode{},
			DefaultRetryNode{},
		},
	}
}
