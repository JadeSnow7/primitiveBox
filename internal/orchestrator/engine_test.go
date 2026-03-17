package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

func TestInferIntent_AppPrimitive_UsesManifest(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:      "myapp",
		Name:       "myapp.search",
		SocketPath: "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	engine := NewEngine(&failingExecutor{})
	engine.SetAppRegistry(reg)

	intent := engine.inferPrimitiveIntent(context.Background(), "myapp.search")
	if intent.Category != cvr.IntentQuery {
		t.Fatalf("expected IntentQuery, got %s", intent.Category)
	}
	if !intent.Reversible {
		t.Fatal("expected Reversible=true")
	}
	if intent.RiskLevel != cvr.RiskLow {
		t.Fatalf("expected RiskLow, got %s", intent.RiskLevel)
	}
}

func TestInferIntent_UnknownPrimitive_DefaultsToMutation(t *testing.T) {
	t.Parallel()

	engine := NewEngine(&failingExecutor{})
	intent := engine.inferPrimitiveIntent(context.Background(), "unknown.primitive")
	if intent.Category != cvr.IntentMutation {
		t.Fatalf("expected IntentMutation for unknown primitive, got %s", intent.Category)
	}
}

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

// rollbackTrackingExecutor records state.restore calls and fails the main primitive once.
type rollbackTrackingExecutor struct {
	checkpointID   string
	restoreCalls   []string // checkpoint IDs passed to state.restore
	mainCallCount  int
}

func (r *rollbackTrackingExecutor) Execute(ctx context.Context, method string, params json.RawMessage) (*StepResult, error) {
	switch method {
	case "state.checkpoint":
		payload, _ := json.Marshal(map[string]any{"checkpoint_id": r.checkpointID})
		return &StepResult{Success: true, Data: payload}, nil
	case "state.restore":
		var p struct {
			CheckpointID string `json:"checkpoint_id"`
		}
		_ = json.Unmarshal(params, &p)
		r.restoreCalls = append(r.restoreCalls, p.CheckpointID)
		return &StepResult{Success: true}, nil
	default:
		r.mainCallCount++
		// Return nil error so CVR gets VerifyOutcomeFailed (not VerifyOutcomeError),
		// allowing IrreversibleMutationNode to fire and return RecoveryActionRollback.
		return &StepResult{
			Success: false,
			Error:   &StepError{Kind: FailureUnknown, Code: "FAIL", Message: "write failed"},
		}, nil
	}
}

func (r *rollbackTrackingExecutor) ListPrimitives() []string {
	return []string{"fs.write", "state.checkpoint", "state.restore"}
}

// mockManifestStore is a minimal in-memory manifest store for engine tests.
type mockManifestStore struct {
	manifests map[string]cvr.CheckpointManifest
}

func newMockManifestStore() *mockManifestStore {
	return &mockManifestStore{manifests: make(map[string]cvr.CheckpointManifest)}
}

func (m *mockManifestStore) SaveManifest(_ context.Context, manifest cvr.CheckpointManifest) error {
	m.manifests[manifest.CheckpointID] = manifest
	return nil
}
func (m *mockManifestStore) GetManifest(_ context.Context, id string) (*cvr.CheckpointManifest, error) {
	v, ok := m.manifests[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (m *mockManifestStore) GetManifestChain(_ context.Context, id string, _ int) ([]cvr.CheckpointManifest, error) {
	v, ok := m.manifests[id]
	if !ok {
		return nil, nil
	}
	return []cvr.CheckpointManifest{v}, nil
}
func (m *mockManifestStore) MarkCorrupted(_ context.Context, id, reason string) error {
	if v, ok := m.manifests[id]; ok {
		v.Corrupted = true
		v.CorruptReason = reason
		m.manifests[id] = v
	}
	return nil
}

func TestCVRCoordinator_IrreversibleMutation_RollbackExecuted(t *testing.T) {
	t.Parallel()

	const wantCheckpointID = "cp-abc123"
	executor := &rollbackTrackingExecutor{checkpointID: wantCheckpointID}
	store := newMockManifestStore()

	engine := NewEngineWithStores(executor, nil, store)
	task := &Task{
		ID:         "task-rollback",
		SandboxID:  "sb-test",
		MaxRetries: 0, // fail on first attempt so rollback fires immediately
	}
	step := &Step{
		ID:        "step-1",
		Primitive: "fs.write",
		Input:     json.RawMessage(`{"path":"main.go","content":"bad"}`),
		Status:    StepPending,
	}

	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected error after rollback + pause")
	}

	// Verify state.restore was called with the correct checkpoint ID.
	if len(executor.restoreCalls) == 0 {
		t.Fatal("expected state.restore to be called, but it was not")
	}
	if executor.restoreCalls[0] != wantCheckpointID {
		t.Fatalf("expected restore checkpoint_id=%q, got %q", wantCheckpointID, executor.restoreCalls[0])
	}

	// Verify step is marked rolled back, not merely failed.
	if step.Status != StepRolledBack {
		t.Fatalf("expected step status StepRolledBack, got %s", step.Status)
	}
}

func TestCVRCoordinator_AbortAction_TerminatesImmediately(t *testing.T) {
	t.Parallel()

	// Use an empty DecisionTree (no nodes → always returns RecoveryActionAbort).
	abortExecutor := &singleFailExecutor{}
	engine := NewEngineWithStores(abortExecutor, nil, newMockManifestStore())
	// Replace the default tree with an empty one that always aborts.
	engine.cvrTree = &cvr.DecisionTree{}
	task := &Task{ID: "task-abort", MaxRetries: 3}
	step := &Step{
		ID:        "step-1",
		Primitive: "fs.write",
		Input:     json.RawMessage(`{}`),
		Status:    StepPending,
	}

	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected terminal error from abort")
	}
	// Only 1 execution — RecoveryActionAbort must not retry.
	if abortExecutor.calls != 1 {
		t.Fatalf("expected 1 execution (no retry on abort), got %d", abortExecutor.calls)
	}
	if !strings.Contains(err.Error(), "terminal failure") {
		t.Fatalf("expected 'terminal failure' in error, got: %v", err)
	}
}

// singleFailExecutor always fails the main primitive, succeeds for checkpoint.
type singleFailExecutor struct{ calls int }

func (s *singleFailExecutor) Execute(_ context.Context, method string, _ json.RawMessage) (*StepResult, error) {
	if method == "state.checkpoint" {
		payload, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-abort"})
		return &StepResult{Success: true, Data: payload}, nil
	}
	s.calls++
	// nil error → VerifyOutcomeFailed so tree can decide (empty tree → abort)
	return &StepResult{
		Success: false,
		Error:   &StepError{Kind: FailureUnknown, Code: "FAIL", Message: "failed"},
	}, nil
}

func (s *singleFailExecutor) ListPrimitives() []string { return []string{"fs.write"} }
