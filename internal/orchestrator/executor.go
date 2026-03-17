// Package orchestrator defines the PrimitiveExecutor interface and task types.
// The executor decouples the orchestrator from the primitive layer.
package orchestrator

import (
	"context"
	"encoding/json"
	"time"
)

// --------------------------------------------------------------------------
// PrimitiveExecutor Interface
// --------------------------------------------------------------------------

// PrimitiveExecutor is the bridge between the Orchestrator and the Primitive Layer.
// It abstracts how primitives are discovered and invoked.
type PrimitiveExecutor interface {
	// Execute invokes a primitive by name with the given parameters.
	Execute(ctx context.Context, method string, params json.RawMessage) (*StepResult, error)

	// ListPrimitives returns all available primitives.
	ListPrimitives() []string
}

// --------------------------------------------------------------------------
// Task & Step Types
// --------------------------------------------------------------------------

// Task represents a high-level unit of work assigned to an AI agent.
type Task struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	SandboxID   string     `json:"sandbox_id"`
	Status      TaskStatus `json:"status"`
	Steps       []Step     `json:"steps"`
	RetryCount  int        `json:"retry_count"`
	MaxRetries  int        `json:"max_retries"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TaskStatus represents the state machine for task execution.
type TaskStatus string

const (
	TaskPlanning   TaskStatus = "PLANNING"
	TaskExecuting  TaskStatus = "EXECUTING"
	TaskVerifying  TaskStatus = "VERIFYING"
	TaskRecovering TaskStatus = "RECOVERING"
	TaskCompleted  TaskStatus = "COMPLETED"
	TaskFailed     TaskStatus = "FAILED"
	TaskPaused     TaskStatus = "PAUSED" // Needs human intervention
)

// Step represents a single primitive invocation within a task.
type Step struct {
	ID           string          `json:"id"`
	Primitive    string          `json:"primitive"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output,omitempty"`
	Result       *StepResult     `json:"result,omitempty"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	Status       StepStatus      `json:"status"`
	Duration     time.Duration   `json:"duration_ms"`
	StartedAt    time.Time       `json:"started_at"`
	CompletedAt  time.Time       `json:"completed_at,omitempty"`
	Error        string          `json:"error,omitempty"`
	Escalated    bool            `json:"escalated,omitempty"`
}

// StepResult wraps the outcome of a single primitive call.
type StepResult struct {
	Success  bool            `json:"success"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *StepError      `json:"error,omitempty"`
	Duration int64           `json:"duration_ms"`
}

// StepError captures structured error information for failure classification.
type StepError struct {
	Kind    FailureKind `json:"kind"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
	// Truncated summary for LLM context (prevent context explosion).
	Summary string `json:"summary"`
}

// StepStatus tracks the execution state of a single step.
type StepStatus string

const (
	StepPending   StepStatus = "PENDING"
	StepRunning   StepStatus = "RUNNING"
	StepPassed    StepStatus = "PASSED"
	StepFailed    StepStatus = "FAILED"
	StepSkipped   StepStatus = "SKIPPED"
	StepRolledBack StepStatus = "ROLLED_BACK"
)

// --------------------------------------------------------------------------
// Failure Classification
// --------------------------------------------------------------------------

// FailureKind categorizes failures to drive recovery strategy selection.
type FailureKind int

const (
	FailureEnvironment FailureKind = iota // Dependency missing, permission issue
	FailureTestFail                        // Test assertion failure
	FailureSyntax                          // Syntax/compilation error
	FailureTimeout                         // Command or operation timed out
	FailureDuplicate                       // Identical retry detected
	FailureUnknown                         // Unclassifiable failure
)

// String returns a human-readable failure kind name.
func (f FailureKind) String() string {
	switch f {
	case FailureEnvironment:
		return "ENVIRONMENT"
	case FailureTestFail:
		return "TEST_FAILURE"
	case FailureSyntax:
		return "SYNTAX_ERROR"
	case FailureTimeout:
		return "TIMEOUT"
	case FailureDuplicate:
		return "DUPLICATE_RETRY"
	case FailureUnknown:
		return "UNKNOWN"
	default:
		return "UNKNOWN"
	}
}
