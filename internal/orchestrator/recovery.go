package orchestrator

// --------------------------------------------------------------------------
// Recovery Policy: Failure classification and recovery strategy
// --------------------------------------------------------------------------

// RecoveryAction defines what to do after a failure.
type RecoveryAction string

const (
	ActionRetry    RecoveryAction = "RETRY"
	ActionPause    RecoveryAction = "PAUSE"    // Needs human intervention
	ActionFail     RecoveryAction = "FAIL"     // Terminal failure, no retry
	ActionContinue RecoveryAction = "CONTINUE" // Skip this step, proceed to next
)

func (a RecoveryAction) String() string { return string(a) }

// RecoveryPolicy implements the failure recovery strategy matrix.
type RecoveryPolicy struct{}

// NewRecoveryPolicy creates a new recovery policy.
func NewRecoveryPolicy() *RecoveryPolicy {
	return &RecoveryPolicy{}
}

// Decide determines the recovery action based on failure kind and attempt count.
//
// Recovery Strategy Matrix:
//
//	Failure Type   | Attempt 1      | Attempt 2      | Attempt 3+
//	---------------|----------------|----------------|----------------
//	Environment    | Auto-fix retry | Switch strategy| PAUSE
//	Test Failure   | Rollback+retry | Rollback+retry | PAUSE
//	Syntax Error   | Auto-fix retry | Auto-fix retry | PAUSE
//	Timeout        | Increase+retry | Simplify       | PAUSE
//	Duplicate      | PAUSE          | —              | —
//	Unknown        | Retry          | Retry          | PAUSE
func (rp *RecoveryPolicy) Decide(kind FailureKind, attempt, maxRetries int) RecoveryAction {
	// Duplicate retries always pause immediately
	if kind == FailureDuplicate {
		return ActionPause
	}

	// If we've exceeded max retries, always pause
	if attempt >= maxRetries {
		return ActionPause
	}

	// For all other failure types within retry limit, try again
	return ActionRetry
}

// TruncateErrorForLLM creates a concise error summary suitable for LLM context.
// This prevents context window explosion from long error logs.
func TruncateErrorForLLM(errorMsg string, maxLen int) string {
	if len(errorMsg) <= maxLen {
		return errorMsg
	}

	// Keep first and last portions for context
	keepLen := maxLen / 2
	return errorMsg[:keepLen] +
		"\n... [error truncated to prevent context overflow] ...\n" +
		errorMsg[len(errorMsg)-keepLen:]
}
