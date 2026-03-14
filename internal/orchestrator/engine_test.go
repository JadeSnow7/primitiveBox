package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestExecuteStepWithRecoveryRetriesUpToMaxRetries(t *testing.T) {
	t.Parallel()

	executor := &failingExecutor{}
	engine := NewEngine(executor)
	task := &Task{ID: "task-1", MaxRetries: 2}
	step := &Step{
		ID:        "step-1",
		Primitive: "fs.write",
		Input:     json.RawMessage(`{"path":"main.txt"}`),
		Status:    StepPending,
	}

	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected failure after retries are exhausted")
	}
	if executor.calls != 3 {
		t.Fatalf("expected 3 attempts total, got %d", executor.calls)
	}
	if step.Status != StepFailed {
		t.Fatalf("expected step to be marked failed, got %s", step.Status)
	}
}

type failingExecutor struct {
	calls int
}

func (f *failingExecutor) Execute(ctx context.Context, method string, params json.RawMessage) (*StepResult, error) {
	f.calls++
	return &StepResult{
		Success: false,
		Error: &StepError{
			Kind:    FailureUnknown,
			Code:    "EXECUTION_ERROR",
			Message: "boom",
		},
	}, errors.New("boom")
}

func (f *failingExecutor) ListPrimitives() []string {
	return []string{"fs.write"}
}
