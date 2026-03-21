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
	checkpointID  string
	restoreCalls  []string // checkpoint IDs passed to state.restore
	mainCallCount int
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

type recordingExecutor struct {
	calls []string
}

func (r *recordingExecutor) Execute(_ context.Context, method string, params json.RawMessage) (*StepResult, error) {
	r.calls = append(r.calls, method)

	switch method {
	case "myapp.mutate":
		payload, _ := json.Marshal(map[string]any{"stored": true})
		return &StepResult{Success: true, Data: payload}, nil
	case "myapp.verify":
		payload, _ := json.Marshal(map[string]any{"passed": true})
		return &StepResult{Success: true, Data: payload}, nil
	case "verify.command":
		payload, _ := json.Marshal(map[string]any{"passed": true, "summary": "ok"})
		return &StepResult{Success: true, Data: payload}, nil
	default:
		payload, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-app-verify"})
		return &StepResult{Success: true, Data: payload}, nil
	}
}

func (r *recordingExecutor) ListPrimitives() []string {
	return []string{"myapp.mutate", "myapp.verify", "verify.command", "state.checkpoint"}
}

func TestExecuteStepWithRecovery_AppPrimitiveVerifyStrategyPrimitive(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "myapp.verify",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &recordingExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-verify", MaxRetries: 0}
	step := &Step{ID: "step-verify", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"ok"}`)}
	result, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err != nil {
		t.Fatalf("execute step: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if len(executor.calls) < 2 || executor.calls[1] != "myapp.verify" {
		t.Fatalf("expected verify primitive to run after main call, got %v", executor.calls)
	}
}

func TestExecuteStepWithRecovery_AppPrimitiveVerifyStrategyCommand(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy: "command",
			Command:  "true",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &recordingExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-command-verify", MaxRetries: 0}
	step := &Step{ID: "step-command-verify", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"ok"}`)}
	if _, err := engine.executeStepWithRecovery(context.Background(), task, step); err != nil {
		t.Fatalf("execute step: %v", err)
	}
	if len(executor.calls) < 2 || executor.calls[1] != "verify.command" {
		t.Fatalf("expected verify.command to run after main call, got %v", executor.calls)
	}
}

func TestExecuteStepWithRecovery_AppPrimitiveVerifyStrategyNoneSkipsAutomaticVerify(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy: "none",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &recordingExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)
	engine.cvrStrategy = fixedEngineStrategy{}

	task := &Task{ID: "task-none", MaxRetries: 0}
	step := &Step{ID: "step-none", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"ok"}`)}
	if _, err := engine.executeStepWithRecovery(context.Background(), task, step); err != nil {
		t.Fatalf("execute step: %v", err)
	}
	if len(executor.calls) != 1 || executor.calls[0] != "myapp.mutate" {
		t.Fatalf("expected no automatic verify calls, got %v", executor.calls)
	}
}

type verifyFailureExecutor struct {
	calls []string
}

func (v *verifyFailureExecutor) Execute(_ context.Context, method string, params json.RawMessage) (*StepResult, error) {
	v.calls = append(v.calls, method)

	switch method {
	case "state.checkpoint":
		payload, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-verify-fail"})
		return &StepResult{Success: true, Data: payload}, nil
	case "state.restore":
		return &StepResult{Success: true}, nil
	case "myapp.mutate":
		payload, _ := json.Marshal(map[string]any{"stored": true})
		return &StepResult{Success: true, Data: payload}, nil
	case "myapp.verify":
		payload, _ := json.Marshal(map[string]any{"passed": false, "summary": "verify failed"})
		return &StepResult{Success: true, Data: payload}, nil
	default:
		return &StepResult{Success: true}, nil
	}
}

func (v *verifyFailureExecutor) ListPrimitives() []string {
	return []string{"myapp.mutate", "myapp.verify", "state.checkpoint", "state.restore"}
}

func TestExecuteStepWithRecovery_VerifyFailureAffectsOutcome(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "myapp.verify",
		},
		Rollback: &primitive.AppPrimitiveRollback{
			Strategy:  "primitive",
			Primitive: "myapp.rollback",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &appRollbackExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-verify-fail", MaxRetries: 0}
	step := &Step{ID: "step-verify-fail", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"bad"}`)}
	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected verify failure to fail the step")
	}
	if !strings.Contains(err.Error(), "verify failed") {
		t.Fatalf("expected verify failure in error, got %v", err)
	}
	if step.Status != StepRolledBack {
		t.Fatalf("expected verify failure rollback, got %s", step.Status)
	}
	if !containsCall(executor.calls, "myapp.rollback") {
		t.Fatalf("expected declared app rollback to run, got %v", executor.calls)
	}
	if containsCall(executor.calls, "state.restore") {
		t.Fatalf("expected app rollback to avoid workspace restore without checkpoint need, got %v", executor.calls)
	}
}

type appRollbackExecutor struct {
	calls        []string
	rollbackBody map[string]any
}

func (v *appRollbackExecutor) Execute(_ context.Context, method string, params json.RawMessage) (*StepResult, error) {
	v.calls = append(v.calls, method)

	switch method {
	case "myapp.mutate":
		payload, _ := json.Marshal(map[string]any{"stored": true, "previous_exists": true, "previous_value": "old"})
		return &StepResult{Success: true, Data: payload}, nil
	case "myapp.verify":
		payload, _ := json.Marshal(map[string]any{"passed": false, "summary": "verify failed"})
		return &StepResult{Success: true, Data: payload}, nil
	case "myapp.rollback":
		_ = json.Unmarshal(params, &v.rollbackBody)
		return &StepResult{Success: true}, nil
	case "state.restore":
		return &StepResult{Success: true}, nil
	default:
		payload, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-verify-fail"})
		return &StepResult{Success: true, Data: payload}, nil
	}
}

func (v *appRollbackExecutor) ListPrimitives() []string {
	return []string{"myapp.mutate", "myapp.verify", "myapp.rollback", "state.checkpoint", "state.restore"}
}

func TestExecuteStepWithRecovery_AppRollbackFailureSurfacesClearly(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "myapp.verify",
		},
		Rollback: &primitive.AppPrimitiveRollback{
			Strategy:  "primitive",
			Primitive: "myapp.rollback",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &failingAppRollbackExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-rollback-fail", MaxRetries: 0}
	step := &Step{ID: "step-rollback-fail", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"bad"}`)}
	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected rollback failure to fail the step")
	}
	if !strings.Contains(err.Error(), "app rollback failed for myapp.mutate via myapp.rollback") {
		t.Fatalf("expected rollback failure in error, got %v", err)
	}
	if step.Status != StepFailed {
		t.Fatalf("expected rollback failure to leave step failed, got %s", step.Status)
	}
}

type failingAppRollbackExecutor struct {
	appRollbackExecutor
}

func (v *failingAppRollbackExecutor) Execute(ctx context.Context, method string, params json.RawMessage) (*StepResult, error) {
	if method == "myapp.rollback" {
		v.calls = append(v.calls, method)
		return nil, errors.New("adapter rollback exploded")
	}
	return v.appRollbackExecutor.Execute(ctx, method, params)
}

func TestExecuteStepWithRecovery_IrreversibleAppPrimitiveWithoutRollbackFailsClosed(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		},
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "myapp.verify",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &verifyFailureExecutor{}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-fail-closed", MaxRetries: 0}
	step := &Step{ID: "step-fail-closed", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"bad"}`)}
	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected fail-closed error")
	}
	if !strings.Contains(err.Error(), "state.restore alone does not recover app state") {
		t.Fatalf("expected fail-closed explanation, got %v", err)
	}
	if containsCall(executor.calls, "state.restore") {
		t.Fatalf("expected fail-closed path to avoid state.restore, got %v", executor.calls)
	}
	if step.Status != StepFailed {
		t.Fatalf("expected fail-closed path to fail step, got %s", step.Status)
	}
}

func TestExecuteStepWithRecovery_RollbackStrategyNonePreservesWorkspaceRestore(t *testing.T) {
	t.Parallel()

	reg := primitive.NewInMemoryAppRegistry()
	if err := reg.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.mutate",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/myapp.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		},
		Rollback: &primitive.AppPrimitiveRollback{
			Strategy: "none",
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	executor := &rollbackTrackingExecutor{checkpointID: "cp-none"}
	engine := NewEngineWithStores(executor, nil, newMockManifestStore())
	engine.SetAppRegistry(reg)

	task := &Task{ID: "task-none-rollback", MaxRetries: 0}
	step := &Step{ID: "step-none-rollback", Primitive: "myapp.mutate", Input: json.RawMessage(`{"value":"bad"}`)}
	_, err := engine.executeStepWithRecovery(context.Background(), task, step)
	if err == nil {
		t.Fatal("expected pause after workspace restore")
	}
	if len(executor.restoreCalls) != 1 || executor.restoreCalls[0] != "cp-none" {
		t.Fatalf("expected state.restore fallback for rollback.strategy=none, got %v", executor.restoreCalls)
	}
	if step.Status != StepRolledBack {
		t.Fatalf("expected rollback.strategy=none to preserve workspace restore behavior, got %s", step.Status)
	}
}

func containsCall(calls []string, method string) bool {
	for _, call := range calls {
		if call == method {
			return true
		}
	}
	return false
}

type fixedEngineStrategy struct{}

func (fixedEngineStrategy) Name() string        { return "fixed" }
func (fixedEngineStrategy) Description() string { return "fixed" }
func (fixedEngineStrategy) Run(ctx context.Context, exec cvr.StrategyExecutor, result cvr.ExecuteResult, manifest *cvr.CheckpointManifest) (cvr.StrategyResult, error) {
	_ = ctx
	_ = exec
	_ = result
	_ = manifest
	return cvr.StrategyResult{
		Outcome: cvr.VerifyOutcomePassed,
		Message: "passed",
	}, nil
}
