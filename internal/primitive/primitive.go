// Package primitive defines the core Primitive interface and types.
// All AI primitives (fs, code, shell, state, verify) implement this interface.
package primitive

import (
	"context"
	"encoding/json"
)

// --------------------------------------------------------------------------
// Core Primitive Interface
// --------------------------------------------------------------------------

// Primitive is the fundamental building block of PrimitiveBox.
// Each primitive represents a single, well-defined operation that an AI agent
// can invoke within a sandboxed environment.
type Primitive interface {
	// Name returns the fully qualified primitive name (e.g., "fs.read", "shell.exec").
	Name() string

	// Category returns the primitive category (e.g., "fs", "shell", "code").
	Category() string

	// Schema returns the JSON Schema definition for this primitive's input/output.
	Schema() Schema

	// Execute runs the primitive with the given parameters.
	// It returns a structured result or an error.
	Execute(ctx context.Context, params json.RawMessage) (Result, error)
}

// --------------------------------------------------------------------------
// Schema & Metadata
// --------------------------------------------------------------------------

// Schema describes the input/output contract of a primitive.
type Schema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`  // JSON Schema for params
	Output      json.RawMessage `json:"output"` // JSON Schema for result
}

// Result wraps the structured output of a primitive execution.
type Result struct {
	Data     any    `json:"data"`               // Structured result data
	Duration int64  `json:"duration_ms"`         // Execution time in ms
	Diff     string `json:"diff,omitempty"`      // File diff if applicable
	Warning  string `json:"warning,omitempty"`   // Non-fatal warning
}

// --------------------------------------------------------------------------
// Execution Context
// --------------------------------------------------------------------------

// ExecContext carries sandbox-specific context for primitive execution.
type ExecContext struct {
	SandboxID    string            `json:"sandbox_id"`
	WorkspaceDir string            `json:"workspace_dir"`
	Env          map[string]string `json:"env,omitempty"`
	Timeout      int               `json:"timeout_s"`      // Default timeout in seconds
	DryRun       bool              `json:"dry_run"`         // If true, simulate only
}

// ContextKey is used to store ExecContext in context.Context.
type contextKey struct{}

// WithExecContext adds ExecContext to a context.Context.
func WithExecContext(ctx context.Context, ec *ExecContext) context.Context {
	return context.WithValue(ctx, contextKey{}, ec)
}

// GetExecContext retrieves ExecContext from a context.Context.
func GetExecContext(ctx context.Context) (*ExecContext, bool) {
	ec, ok := ctx.Value(contextKey{}).(*ExecContext)
	return ec, ok
}

// --------------------------------------------------------------------------
// Error Types
// --------------------------------------------------------------------------

// PrimitiveError represents a structured error from primitive execution.
type PrimitiveError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
}

func (e *PrimitiveError) Error() string {
	return e.Message
}

// ErrorCode categorizes primitive errors.
type ErrorCode string

const (
	ErrNotFound       ErrorCode = "NOT_FOUND"
	ErrPermission     ErrorCode = "PERMISSION_DENIED"
	ErrTimeout        ErrorCode = "TIMEOUT"
	ErrValidation     ErrorCode = "VALIDATION_ERROR"
	ErrExecution      ErrorCode = "EXECUTION_ERROR"
	ErrResourceLimit  ErrorCode = "RESOURCE_LIMIT"
	ErrInternal       ErrorCode = "INTERNAL_ERROR"
)
