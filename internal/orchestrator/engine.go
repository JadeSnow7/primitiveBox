// Package orchestrator implements the task engine with plan-execute-verify-recover loop.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// --------------------------------------------------------------------------
// Engine: Task Execution Engine
// --------------------------------------------------------------------------

// Engine orchestrates plan → execute → verify → recover loops.
type Engine struct {
	executor PrimitiveExecutor
	state    *StateTracker
	recovery *RecoveryPolicy
}

// NewEngine creates a new orchestrator engine.
func NewEngine(executor PrimitiveExecutor) *Engine {
	return &Engine{
		executor: executor,
		state:    NewStateTracker(),
		recovery: NewRecoveryPolicy(),
	}
}

// RunTask executes a task through the full orchestration lifecycle.
func (e *Engine) RunTask(ctx context.Context, task *Task) error {
	task.Status = TaskExecuting
	task.UpdatedAt = time.Now()
	e.state.TrackTask(task)

	log.Printf("[Engine] Starting task %s: %s", task.ID, task.Description)

	for i, step := range task.Steps {
		log.Printf("[Engine] Step %d/%d: %s", i+1, len(task.Steps), step.Primitive)

		// Execute step with retry logic
		result, err := e.executeStepWithRecovery(ctx, task, &task.Steps[i])
		if err != nil {
			task.Status = TaskPaused
			task.UpdatedAt = time.Now()
			log.Printf("[Engine] Task %s paused: %v", task.ID, err)
			return fmt.Errorf("task paused at step %d: %w", i+1, err)
		}

		// Record step result
		task.Steps[i].Result = result
		task.Steps[i].Status = StepPassed
		task.Steps[i].CompletedAt = time.Now()
	}

	task.Status = TaskCompleted
	task.UpdatedAt = time.Now()
	log.Printf("[Engine] Task %s completed successfully", task.ID)
	return nil
}

// executeStepWithRecovery executes a step and applies recovery on failure.
func (e *Engine) executeStepWithRecovery(ctx context.Context, task *Task, step *Step) (*StepResult, error) {
	maxRetries := 3
	if task.MaxRetries > 0 {
		maxRetries = task.MaxRetries
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[Engine] Retry %d/%d for step %s", attempt, maxRetries, step.Primitive)
		}

		step.Status = StepRunning
		step.StartedAt = time.Now()

		// Execute the primitive
		result, err := e.executor.Execute(ctx, step.Primitive, step.Input)
		duration := time.Since(step.StartedAt)
		step.Duration = duration

		if err == nil && result.Success {
			return result, nil
		}

		// Classify failure
		lastErr = err
		var failureKind FailureKind
		if result != nil && result.Error != nil {
			failureKind = result.Error.Kind
		} else {
			failureKind = FailureUnknown
		}

		// Apply recovery strategy
		action := e.recovery.Decide(failureKind, attempt, maxRetries)
		log.Printf("[Engine] Failure kind=%s, action=%s", failureKind, action)

		switch action {
		case ActionRetry:
			continue
		case ActionPause:
			step.Status = StepFailed
			return nil, fmt.Errorf("max retries exceeded for %s: %v", step.Primitive, lastErr)
		}
	}

	return nil, fmt.Errorf("step %s failed after %d attempts: %v", step.Primitive, maxRetries, lastErr)
}

// CreateTask builds a new task from a description and step definitions.
func (e *Engine) CreateTask(description string, sandboxID string, steps []StepDef) *Task {
	task := &Task{
		ID:          "task-" + uuid.New().String()[:8],
		Description: description,
		SandboxID:   sandboxID,
		Status:      TaskPlanning,
		MaxRetries:  3,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	for _, sd := range steps {
		params, _ := json.Marshal(sd.Params)
		task.Steps = append(task.Steps, Step{
			ID:        "step-" + uuid.New().String()[:8],
			Primitive: sd.Primitive,
			Input:     params,
			Status:    StepPending,
		})
	}

	return task
}

// StepDef is a convenience type for defining task steps.
type StepDef struct {
	Primitive string
	Params    any
}
