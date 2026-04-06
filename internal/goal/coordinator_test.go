package goal

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/orchestrator"
)

// openTestStore returns a fresh SQLiteGoalStore + eventing.Bus for each test.
func openTestStore(t *testing.T) (*control.SQLiteGoalStore, *eventing.Bus) {
	t.Helper()
	store, err := control.OpenSQLiteStore(t.TempDir() + "/coord_test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bus := eventing.NewBus(store)
	return control.NewSQLiteGoalStore(store.DB()), bus
}

// ── Coordinator Execute ───────────────────────────────────────────────────────

func TestGoalCoordinator_Execute_Success(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-exec-1",
		Description: "echo test",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-e1", GoalID: g.ID, Primitive: "noop.success", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, Seq: 1},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &fakeExecutor{successMethods: map[string]bool{"noop.success": true}}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	if err := coord.Execute(ctx, g.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalCompleted {
		t.Errorf("expected completed, got %q", got.Status)
	}

	gotSteps, _ := gs.ListGoalSteps(ctx, g.ID)
	if gotSteps[0].Status != control.GoalStepPassed {
		t.Errorf("step status: expected passed, got %q", gotSteps[0].Status)
	}
}

func TestGoalCoordinator_Execute_StepFailure_GoalFailed(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-exec-fail",
		Description: "failing step",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-fail1", GoalID: g.ID, Primitive: "noop.fail", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, Seq: 1},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &fakeExecutor{successMethods: map[string]bool{}}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	// Execute returns non-nil error (step failed), goal should be marked failed or paused.
	_ = coord.Execute(ctx, g.ID)

	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalFailed && got.Status != control.GoalPaused {
		t.Errorf("expected failed or paused, got %q", got.Status)
	}
}

func TestGoalCoordinator_Execute_AlreadyExecuting(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{ID: "goal-already-exec", Description: "already exec", Status: control.GoalExecuting, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	err := coord.Execute(ctx, g.ID)
	if err == nil {
		t.Fatal("expected error for already-executing goal")
	}
}

func TestGoalCoordinator_Execute_NotFound(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	err := coord.Execute(ctx, "nonexistent-goal")
	if err == nil {
		t.Fatal("expected error for non-existent goal")
	}
}

func TestGoalCoordinator_Execute_CompletesAfterVerificationPasses(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-verify-pass",
		Description: "verification pass",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-vp-1", GoalID: g.ID, Primitive: "noop.success", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, Seq: 1},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := gs.AppendGoalVerification(ctx, &control.GoalVerification{
		ID:          "verify-pass-1",
		GoalID:      g.ID,
		Status:      control.GoalVerificationPending,
		CheckType:   "primitive_call",
		CheckParams: json.RawMessage(`{"method":"verify.ok","expect":{"path":"ok","operator":"eq","expected":true}}`),
	}, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	executor := &fakeExecutor{
		successMethods: map[string]bool{"noop.success": true, "verify.ok": true},
		resultByMethod: map[string]json.RawMessage{"verify.ok": json.RawMessage(`{"ok":true}`)},
	}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	if err := coord.Execute(ctx, g.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalCompleted {
		t.Fatalf("goal status: got %q, want completed", got.Status)
	}
	verifications, _ := gs.ListGoalVerifications(ctx, g.ID)
	if verifications[0].Status != control.GoalVerificationPassed {
		t.Fatalf("verification status: got %q, want passed", verifications[0].Status)
	}
}

func TestGoalCoordinator_Execute_FailsWhenVerificationFails(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-verify-fail",
		Description: "verification fail",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-vf-1", GoalID: g.ID, Primitive: "noop.success", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, Seq: 1},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := gs.AppendGoalVerification(ctx, &control.GoalVerification{
		ID:          "verify-fail-1",
		GoalID:      g.ID,
		Status:      control.GoalVerificationPending,
		CheckType:   "primitive_call",
		CheckParams: json.RawMessage(`{"method":"verify.bad","expect":{"path":"ok","operator":"eq","expected":true}}`),
	}, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	executor := &fakeExecutor{
		successMethods: map[string]bool{"noop.success": true, "verify.bad": true},
		resultByMethod: map[string]json.RawMessage{"verify.bad": json.RawMessage(`{"ok":false}`)},
	}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	err := coord.Execute(ctx, g.ID)
	if err == nil {
		t.Fatal("expected verification failure")
	}

	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalFailed {
		t.Fatalf("goal status: got %q, want failed", got.Status)
	}
	verifications, _ := gs.ListGoalVerifications(ctx, g.ID)
	if verifications[0].Status != control.GoalVerificationFailed {
		t.Fatalf("verification status: got %q, want failed", verifications[0].Status)
	}
}

func TestGoalCoordinator_Execute_FailsClosedOnUnsupportedVerificationType(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-verify-unsupported",
		Description: "verification unsupported",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := gs.AppendGoalVerification(ctx, &control.GoalVerification{
		ID:          "verify-unsupported-1",
		GoalID:      g.ID,
		Status:      control.GoalVerificationPending,
		CheckType:   "shell_script",
		CheckParams: json.RawMessage(`{}`),
	}, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	err := coord.Execute(ctx, g.ID)
	if err == nil {
		t.Fatal("expected unsupported verification to fail closed")
	}

	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalFailed {
		t.Fatalf("goal status: got %q, want failed", got.Status)
	}
}

// ── Coordinator Replay ────────────────────────────────────────────────────────

func TestGoalCoordinator_Replay_Full(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-replay-coord",
		Description: "replay test",
		Status:      control.GoalCompleted,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-r1", GoalID: g.ID, Primitive: "fs.read", Input: json.RawMessage(`{}`), Status: control.GoalStepPassed, Seq: 1},
		{ID: "step-r2", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{}`), Status: control.GoalStepFailed, Seq: 2},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	result, err := coord.Replay(ctx, g.ID, orchestrator.ReplayFull)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if result.GoalID != g.ID {
		t.Errorf("goal_id: got %q", result.GoalID)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	if result.Entries[0].Primitive != "fs.read" {
		t.Errorf("entry[0] primitive: got %q", result.Entries[0].Primitive)
	}
}

func TestGoalCoordinator_Replay_SkipPassed(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-replay-skip",
		Description: "skip passed",
		Status:      control.GoalCompleted,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-s1", GoalID: g.ID, Primitive: "fs.read", Input: json.RawMessage(`{}`), Status: control.GoalStepPassed, Seq: 1},
		{ID: "step-s2", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{}`), Status: control.GoalStepFailed, Seq: 2},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	result, err := coord.Replay(ctx, g.ID, orchestrator.ReplaySkipPassed)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	if !result.Entries[0].Skipped {
		t.Errorf("expected first entry (passed step) to be skipped in skip_passed mode")
	}
}

func TestGoalCoordinator_Replay_NotFound(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	_, err := coord.Replay(ctx, "nonexistent", orchestrator.ReplayFull)
	if err == nil {
		t.Fatal("expected error for non-existent goal")
	}
}

// ── Test double ───────────────────────────────────────────────────────────────

// fakeExecutor simulates a PrimitiveExecutor for coordinator tests.
// Methods listed in successMethods succeed; all others fail.
type fakeExecutor struct {
	mu             sync.Mutex
	calledMethods  []string
	successMethods map[string]bool
	resultByMethod map[string]json.RawMessage
	errByMethod    map[string]error
}

// called reports whether method was invoked at least once.
func (f *fakeExecutor) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.calledMethods {
		if m == method {
			return true
		}
	}
	return false
}

func (f *fakeExecutor) Execute(_ context.Context, method string, _ json.RawMessage) (*orchestrator.StepResult, error) {
	f.mu.Lock()
	f.calledMethods = append(f.calledMethods, method)
	f.mu.Unlock()

	if f.errByMethod != nil && f.errByMethod[method] != nil {
		return &orchestrator.StepResult{
			Success: false,
			Error: &orchestrator.StepError{
				Kind:    orchestrator.FailureUnknown,
				Code:    "FAKE_ERROR",
				Message: f.errByMethod[method].Error(),
			},
		}, f.errByMethod[method]
	}
	if f.successMethods != nil && f.successMethods[method] {
		data := json.RawMessage(`{"ok":true}`)
		if f.resultByMethod != nil && len(f.resultByMethod[method]) > 0 {
			data = f.resultByMethod[method]
		}
		return &orchestrator.StepResult{Success: true, Data: data}, nil
	}
	if method == "state.checkpoint" {
		data, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-fake"})
		return &orchestrator.StepResult{Success: true, Data: data}, nil
	}
	return &orchestrator.StepResult{
		Success: false,
		Error: &orchestrator.StepError{
			Kind:    orchestrator.FailureUnknown,
			Code:    "FAKE_ERROR",
			Message: "fake failure",
		},
	}, nil
}

func (f *fakeExecutor) ListPrimitives() []string { return nil }

// ── Review / Resume tests ─────────────────────────────────────────────────────

func TestGoalCoordinator_Execute_PausesOnHighRiskStep(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-pause-1",
		Description: "high risk test",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-safe", GoalID: g.ID, Primitive: "fs.read", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, RiskLevel: "low", Reversible: true, Seq: 1},
		{ID: "step-risky", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{"command":"rm -rf /"}`), Status: control.GoalStepPending, RiskLevel: "high", Reversible: false, Seq: 2},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &fakeExecutor{successMethods: map[string]bool{"fs.read": true}}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	if err := coord.Execute(ctx, g.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Goal should be paused.
	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalPaused {
		t.Errorf("expected paused, got %q", got.Status)
	}

	// First step should have passed, second awaiting_review.
	gotSteps, _ := gs.ListGoalSteps(ctx, g.ID)
	if gotSteps[0].Status != control.GoalStepPassed {
		t.Errorf("step[0] expected passed, got %q", gotSteps[0].Status)
	}
	if gotSteps[1].Status != control.GoalStepAwaitingReview {
		t.Errorf("step[1] expected awaiting_review, got %q", gotSteps[1].Status)
	}

	// A pending review record should exist.
	reviews, _ := gs.ListGoalReviews(ctx, g.ID)
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	if reviews[0].Status != control.GoalReviewPending {
		t.Errorf("review status: got %q, want pending", reviews[0].Status)
	}
}

func TestGoalCoordinator_Resume_AfterApprove(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-resume-1",
		Description: "resume test",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-rs1", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, RiskLevel: "high", Reversible: false, Seq: 1},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &fakeExecutor{successMethods: map[string]bool{"shell.exec": true}}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	// Execute should pause at the high-risk step.
	if err := coord.Execute(ctx, g.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalPaused {
		t.Fatalf("expected paused, got %q", got.Status)
	}

	// Approve the review.
	reviews, _ := gs.ListGoalReviews(ctx, g.ID)
	if err := gs.DecideGoalReview(ctx, reviews[0].ID, control.GoalReviewApproved, "approved", bus); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Resume should execute the step and complete the goal.
	if err := coord.Resume(ctx, g.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	got, _, _ = gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalCompleted {
		t.Errorf("expected completed, got %q", got.Status)
	}
	gotSteps, _ := gs.ListGoalSteps(ctx, g.ID)
	if gotSteps[0].Status != control.GoalStepPassed {
		t.Errorf("step expected passed, got %q", gotSteps[0].Status)
	}
}

func TestGoalCoordinator_Resume_FailsWithPendingReview(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-resume-pending",
		Description: "pending review block",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	steps := []*control.GoalStep{
		{ID: "step-rp1", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{}`), Status: control.GoalStepPending, RiskLevel: "high", Reversible: false, Seq: 1},
	}
	_ = gs.CreateGoalFull(ctx, g, steps, bus)

	executor := &fakeExecutor{successMethods: map[string]bool{}}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	_ = coord.Execute(ctx, g.ID) // pauses and creates pending review

	// Resume without approving should fail.
	err := coord.Resume(ctx, g.ID)
	if err == nil {
		t.Fatal("expected error when pending review exists")
	}
}

func TestGoalCoordinator_Resume_NotPaused(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-resume-active",
		Description: "not paused",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	_ = gs.CreateGoal(ctx, g, bus)

	engine := orchestrator.NewEngine(&fakeExecutor{})
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	err := coord.Resume(ctx, g.ID)
	if err == nil {
		t.Fatal("expected error resuming non-paused goal")
	}
}

// TestGoalCoordinator_Execute_PreCheckpointCalledForMutationStep verifies that
// executing a goal step routes through the full CVR path (Engine.ExecuteStepViaCVR),
// which triggers state.checkpoint before a mutation primitive is executed.
func TestGoalCoordinator_Execute_PreCheckpointCalledForMutationStep(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()

	g := &control.Goal{
		ID:          "goal-cvr-checkpoint",
		Description: "cvr checkpoint verification",
		Status:      control.GoalCreated,
		Packages:    []string{},
		SandboxIDs:  []string{},
	}
	// shell.exec is inferred by the engine as mutation/irreversible/high →
	// CVR coordinator must issue state.checkpoint before executing it.
	// RiskLevel is "low" so the human-review gate is skipped.
	steps := []*control.GoalStep{
		{
			ID:         "step-cvr-1",
			GoalID:     g.ID,
			Primitive:  "shell.exec",
			Input:      json.RawMessage(`{"command":"echo hello"}`),
			Status:     control.GoalStepPending,
			RiskLevel:  "low",
			Reversible: false,
			Seq:        1,
		},
	}
	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create: %v", err)
	}

	executor := &fakeExecutor{
		successMethods: map[string]bool{"shell.exec": true},
	}
	engine := orchestrator.NewEngine(executor)
	coord := NewGoalCoordinator(gs, engine, bus, nil)

	if err := coord.Execute(ctx, g.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify goal completed.
	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != control.GoalCompleted {
		t.Errorf("goal status: got %q, want completed", got.Status)
	}

	// The critical assertion: state.checkpoint must have been called, proving
	// that execution went through Engine.ExecuteStepViaCVR → CVRCoordinator
	// rather than the bypass path (ExecutorExecute).
	if !executor.called("state.checkpoint") {
		t.Error("state.checkpoint was never called — goal step did not go through the CVR path")
	}
}
