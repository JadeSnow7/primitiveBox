package cvr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCVRCoordinator_IrreversibleMutation(t *testing.T) {
	t.Parallel()

	store := &mockManifestStore{}
	exec := &mockStrategyExecutor{
		result:       ExecuteResult{Success: false, ErrMsg: "write failed"},
		checkpointID: "real-checkpoint-id",
	}
	coordinator := NewCVRCoordinator(store, nil, NewDefaultDecisionTree())

	result, err := coordinator.Execute(context.Background(), CVRRequest{
		PrimitiveID: "fs.write",
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: false,
			RiskLevel:  RiskHigh,
		},
		Exec:    exec,
		Attempt: 0,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.AppliedAction != RecoveryActionRollback {
		t.Fatalf("expected rollback, got %s", result.AppliedAction)
	}
	if result.CheckpointID == "" {
		t.Fatalf("expected checkpoint id to be created")
	}
	if result.LayerAOutcome != "checkpoint_created" {
		t.Fatalf("unexpected layer A outcome: %s", result.LayerAOutcome)
	}
	manifest, err := store.GetManifest(context.Background(), result.CheckpointID)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil {
		t.Fatalf("expected manifest for checkpoint %q", result.CheckpointID)
	}
}

func TestCVRCoordinator_ManifestHasSandboxID(t *testing.T) {
	t.Parallel()

	const wantSandboxID = "sb-test-123"
	store := &mockManifestStore{}
	exec := &mockStrategyExecutor{
		result:       ExecuteResult{Success: false, ErrMsg: "write failed"},
		checkpointID: "cp-sandbox-test",
	}
	coordinator := NewCVRCoordinator(store, nil, NewDefaultDecisionTree())

	result, err := coordinator.Execute(context.Background(), CVRRequest{
		PrimitiveID: "fs.write",
		SandboxID:   wantSandboxID,
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: false,
			RiskLevel:  RiskHigh,
		},
		Exec:    exec,
		Attempt: 0,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.CheckpointID == "" {
		t.Fatal("expected checkpoint to be created")
	}

	manifest, err := store.GetManifest(context.Background(), result.CheckpointID)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil {
		t.Fatalf("manifest not found for checkpoint %q", result.CheckpointID)
	}
	if manifest.SandboxID != wantSandboxID {
		t.Fatalf("expected SandboxID=%q, got %q", wantSandboxID, manifest.SandboxID)
	}
}

func TestCVRCoordinator_LayerAShortCircuit(t *testing.T) {
	t.Parallel()

	store := &mockManifestStore{saveErr: errors.New("disk full")}
	exec := &mockStrategyExecutor{}
	coordinator := NewCVRCoordinator(store, nil, NewDefaultDecisionTree())

	_, err := coordinator.Execute(context.Background(), CVRRequest{
		PrimitiveID: "fs.write",
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: false,
		},
		Exec: exec,
	})
	var layerErr *LayerAErr
	if !errors.As(err, &layerErr) {
		t.Fatalf("expected LayerAErr, got %v", err)
	}
	if exec.calls != 0 {
		t.Fatalf("expected exec not to be called, got %d", exec.calls)
	}
}

func TestCVRCoordinator_DepthExceeded(t *testing.T) {
	t.Parallel()

	exec := &mockStrategyExecutor{}
	coordinator := NewCVRCoordinator(&mockManifestStore{}, nil, NewDefaultDecisionTree())

	_, err := coordinator.Execute(context.Background(), CVRRequest{
		PrimitiveID: "fs.write",
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: false,
		},
		Exec:     exec,
		CVRDepth: MaxCVRDepth,
	})
	if !errors.Is(err, ErrCVRDepthExceeded) {
		t.Fatalf("expected ErrCVRDepthExceeded, got %v", err)
	}
	if exec.calls != 0 {
		t.Fatalf("expected exec not to be called, got %d", exec.calls)
	}
}

func TestDecisionTree_Priority(t *testing.T) {
	t.Parallel()

	tree := NewDefaultDecisionTree()
	action := tree.Decide(RecoveryCtx{
		FailureKind: FailureKindVerifyFail,
		Attempt:     0,
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: false,
		},
		StrategyResult: StrategyResult{Outcome: VerifyOutcomeFailed},
		MaxRetries:     3,
	})
	if action != RecoveryActionRollback {
		t.Fatalf("expected rollback, got %s", action)
	}
}

func TestCVRCoordinator_DoesNotRunVerifyStrategyWhenExecutionFails(t *testing.T) {
	t.Parallel()

	exec := &mockStrategyExecutor{
		result: ExecuteResult{Success: false, ErrMsg: "write failed"},
	}
	verify := &countingVerifyStrategy{}
	coordinator := NewCVRCoordinator(&mockManifestStore{}, verify, NewDefaultDecisionTree())

	result, err := coordinator.Execute(context.Background(), CVRRequest{
		PrimitiveID: "myapp.mutate",
		Intent: PrimitiveIntent{
			Category:   IntentMutation,
			Reversible: true,
			RiskLevel:  RiskMedium,
		},
		Exec: exec,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if verify.calls != 0 {
		t.Fatalf("expected verify strategy to be skipped, got %d calls", verify.calls)
	}
	if result.StrategyResult.Outcome != VerifyOutcomeFailed {
		t.Fatalf("expected execution failure outcome to be preserved, got %s", result.StrategyResult.Outcome)
	}
}

type mockManifestStore struct {
	saveErr   error
	manifests map[string]CheckpointManifest
}

func (m *mockManifestStore) SaveManifest(ctx context.Context, manifest CheckpointManifest) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	if m.manifests == nil {
		m.manifests = make(map[string]CheckpointManifest)
	}
	m.manifests[manifest.CheckpointID] = manifest
	return nil
}

func (m *mockManifestStore) GetManifest(ctx context.Context, checkpointID string) (*CheckpointManifest, error) {
	manifest, ok := m.manifests[checkpointID]
	if !ok {
		return nil, nil
	}
	return &manifest, nil
}

func (m *mockManifestStore) GetManifestChain(ctx context.Context, checkpointID string, maxDepth int) ([]CheckpointManifest, error) {
	manifest, ok := m.manifests[checkpointID]
	if !ok {
		return nil, nil
	}
	return []CheckpointManifest{manifest}, nil
}

func (m *mockManifestStore) MarkCorrupted(ctx context.Context, checkpointID string, reason string) error {
	manifest, ok := m.manifests[checkpointID]
	if !ok {
		return nil
	}
	manifest.Corrupted = true
	manifest.CorruptReason = reason
	m.manifests[checkpointID] = manifest
	return nil
}

type mockStrategyExecutor struct {
	calls        int
	result       ExecuteResult
	err          error
	checkpointID string
}

type countingVerifyStrategy struct {
	calls int
}

func (s *countingVerifyStrategy) Name() string        { return "counting" }
func (s *countingVerifyStrategy) Description() string { return "counting" }
func (s *countingVerifyStrategy) Run(ctx context.Context, exec StrategyExecutor, result ExecuteResult, manifest *CheckpointManifest) (StrategyResult, error) {
	_ = ctx
	_ = exec
	_ = result
	_ = manifest
	s.calls++
	return StrategyResult{Outcome: VerifyOutcomePassed}, nil
}

func (m *mockStrategyExecutor) Execute(ctx context.Context, method string, params any) (ExecuteResult, error) {
	if method == "state.checkpoint" {
		checkpointID := m.checkpointID
		if checkpointID == "" {
			checkpointID = "test-checkpoint-id"
		}
		idJSON, err := json.Marshal(checkpointID)
		if err != nil {
			return ExecuteResult{}, err
		}
		return ExecuteResult{
			Success: true,
			Data: map[string]json.RawMessage{
				"checkpoint_id": idJSON,
			},
		}, nil
	}
	m.calls++
	return m.result, m.err
}
