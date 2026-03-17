package cvr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTestSuiteStrategy_TooSmall(t *testing.T) {
	t.Parallel()

	strategy := &TestSuiteStrategy{MinTests: 3, Command: "go test ./..."}
	result, err := strategy.Run(context.Background(), &strategyExecutorStub{
		result: executeResultFromMap(map[string]any{
			"passed": true,
			"total":  0,
		}),
	}, ExecuteResult{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != VerifyOutcomeFailed {
		t.Fatalf("expected failed outcome, got %+v", result)
	}
	if !strings.Contains(result.Message, "too small") {
		t.Fatalf("expected too small message, got %+v", result)
	}
}

func TestTestSuiteStrategy_Passed(t *testing.T) {
	t.Parallel()

	strategy := &TestSuiteStrategy{Command: "go test ./..."}
	result, err := strategy.Run(context.Background(), &strategyExecutorStub{
		result: executeResultFromMap(map[string]any{
			"passed": true,
			"total":  5,
		}),
	}, ExecuteResult{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != VerifyOutcomePassed {
		t.Fatalf("expected passed outcome, got %+v", result)
	}
}

func TestTestSuiteStrategy_Rollback(t *testing.T) {
	t.Parallel()

	strategy := &TestSuiteStrategy{Command: "go test ./..."}
	result, err := strategy.Run(context.Background(), &strategyExecutorStub{
		result: executeResultFromMap(map[string]any{
			"passed":   false,
			"total":    5,
			"failures": []string{"TestFoo"},
		}),
	}, ExecuteResult{}, &CheckpointManifest{
		Intent: PrimitiveIntent{Reversible: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != VerifyOutcomeFailed {
		t.Fatalf("expected failed outcome, got %+v", result)
	}
	if result.RecoverHint != RecoverHintRollback {
		t.Fatalf("expected rollback, got %+v", result)
	}
}

func TestTestSuiteStrategy_Escalate(t *testing.T) {
	t.Parallel()

	strategy := &TestSuiteStrategy{Command: "go test ./..."}
	result, err := strategy.Run(context.Background(), &strategyExecutorStub{
		result: executeResultFromMap(map[string]any{
			"passed":   false,
			"total":    5,
			"failures": []string{"TestFoo"},
		}),
	}, ExecuteResult{}, &CheckpointManifest{
		Intent: PrimitiveIntent{Reversible: false},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.RecoverHint != RecoverHintEscalate {
		t.Fatalf("expected escalate, got %+v", result)
	}
}

type strategyExecutorStub struct {
	result ExecuteResult
}

func (s *strategyExecutorStub) Execute(ctx context.Context, method string, params any) (ExecuteResult, error) {
	return s.result, nil
}

func executeResultFromMap(values map[string]any) ExecuteResult {
	result := ExecuteResult{
		Success: true,
		Data:    make(map[string]json.RawMessage, len(values)),
	}
	for key, value := range values {
		data, _ := json.Marshal(value)
		result.Data[key] = data
	}
	return result
}
