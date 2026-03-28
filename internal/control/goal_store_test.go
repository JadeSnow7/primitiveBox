package control

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"primitivebox/internal/eventing"
)

func openTestGoalStore(t *testing.T) (*SQLiteGoalStore, *eventing.Bus) {
	t.Helper()
	store, err := OpenSQLiteStore(t.TempDir() + "/goal_test.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bus := eventing.NewBus(store)
	return NewSQLiteGoalStore(store.DB()), bus
}

func TestGoalStore_CreateAndGet(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{
		ID:          "goal-test-1",
		Description: "Deploy postgres + app",
		Status:      GoalCreated,
		Packages:    []string{"postgres", "myapp"},
		SandboxIDs:  []string{"sb-123"},
	}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	got, found, err := gs.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if !found {
		t.Fatal("expected goal to be found")
	}
	if got.Description != g.Description {
		t.Errorf("description: got %q, want %q", got.Description, g.Description)
	}
	if got.Status != GoalCreated {
		t.Errorf("status: got %q, want %q", got.Status, GoalCreated)
	}
	if len(got.Packages) != 2 || got.Packages[0] != "postgres" {
		t.Errorf("packages: got %v", got.Packages)
	}
	if len(got.SandboxIDs) != 1 || got.SandboxIDs[0] != "sb-123" {
		t.Errorf("sandbox_ids: got %v", got.SandboxIDs)
	}
}

func TestGoalStore_ListGoals(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	for i, desc := range []string{"goal A", "goal B", "goal C"} {
		g := &Goal{
			ID:          string(rune('a'+i)) + "-id",
			Description: desc,
			Status:      GoalCreated,
			Packages:    []string{},
			SandboxIDs:  []string{},
		}
		if err := gs.CreateGoal(ctx, g, bus); err != nil {
			t.Fatalf("create goal: %v", err)
		}
	}

	goals, err := gs.ListGoals(ctx)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	if len(goals) != 3 {
		t.Errorf("expected 3 goals, got %d", len(goals))
	}
}

func TestGoalStore_UpdateGoalStatus(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-status-test", Description: "status test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if err := gs.UpdateGoalStatus(ctx, g.ID, GoalExecuting, bus); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != GoalExecuting {
		t.Errorf("expected status %q, got %q", GoalExecuting, got.Status)
	}
}

func TestGoalStore_WriteAndEmitOrdering(t *testing.T) {
	t.Parallel()
	store, err := OpenSQLiteStore(t.TempDir() + "/ordering_test.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	bus := eventing.NewBus(store)
	gs := NewSQLiteGoalStore(store.DB())
	ctx := context.Background()

	var mu sync.Mutex
	var dbRowExistedAtEmit bool

	ch, unsub := bus.Subscribe(8)
	defer unsub()

	g := &Goal{ID: "goal-order-1", Description: "order test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}

	// Start a goroutine that waits for the event, then checks the DB.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if ev.Type == EventGoalCreated {
				// DB must have the row already.
				row := store.DB().QueryRow(`SELECT id FROM goals WHERE id = ?`, g.ID)
				var id string
				err := row.Scan(&id)
				mu.Lock()
				dbRowExistedAtEmit = (err == nil && id == g.ID)
				mu.Unlock()
				return
			}
		}
	}()

	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	<-done
	mu.Lock()
	existed := dbRowExistedAtEmit
	mu.Unlock()
	if !existed {
		t.Error("event was emitted before DB row was written")
	}
}

func TestGoalStore_AppendGoalStep(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-step-test", Description: "step test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	step := &GoalStep{
		ID:        "step-001",
		GoalID:    g.ID,
		Primitive: "shell.exec",
		Input:     json.RawMessage(`{"command":"echo hello"}`),
		Status:    GoalStepPending,
		Seq:       1,
	}
	if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
		t.Fatalf("append goal step: %v", err)
	}

	steps, err := gs.ListGoalSteps(ctx, g.ID)
	if err != nil {
		t.Fatalf("list goal steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Primitive != "shell.exec" {
		t.Errorf("primitive: got %q, want shell.exec", steps[0].Primitive)
	}
	if steps[0].Seq != 1 {
		t.Errorf("seq: got %d, want 1", steps[0].Seq)
	}
}

func TestGoalStore_AppendGoalVerification(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-verify-test", Description: "verify test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	v := &GoalVerification{
		ID:     "verify-001",
		GoalID: g.ID,
		Status: GoalVerificationPending,
	}
	if err := gs.AppendGoalVerification(ctx, v, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	verifications, err := gs.ListGoalVerifications(ctx, g.ID)
	if err != nil {
		t.Fatalf("list verifications: %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("expected 1 verification, got %d", len(verifications))
	}
	if verifications[0].Status != GoalVerificationPending {
		t.Errorf("status: got %q, want pending", verifications[0].Status)
	}
}

func TestGoalStore_ReplayGoal(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-replay-test", Description: "replay test", Status: GoalCompleted, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	for i := 0; i < 2; i++ {
		step := &GoalStep{
			ID:        string(rune('a'+i)) + "-step",
			GoalID:    g.ID,
			Primitive: "fs.read",
			Input:     json.RawMessage(`{}`),
			Status:    GoalStepPassed,
			Seq:       i + 1,
		}
		if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
			t.Fatalf("append step: %v", err)
		}
	}

	ch, unsub := bus.Subscribe(8)
	defer unsub()

	var events []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if ev.Type == EventGoalReplayStarted || ev.Type == EventGoalReplayCompleted {
				events = append(events, ev.Type)
			}
			if ev.Type == EventGoalReplayCompleted {
				return
			}
		}
	}()

	if err := gs.ReplayGoal(ctx, g.ID, bus); err != nil {
		t.Fatalf("replay goal: %v", err)
	}
	<-done

	if len(events) < 2 {
		t.Errorf("expected replay_started and replay_completed events, got %v", events)
	}
}

func TestGoalStore_GetNotFound(t *testing.T) {
	t.Parallel()
	gs, _ := openTestGoalStore(t)
	ctx := context.Background()

	got, found, err := gs.GetGoal(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Errorf("expected not found, got: %+v", got)
	}
}

func TestGoalBinding_AppendAndList(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-bind-test", Description: "binding test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	b1 := &GoalBinding{ID: "bind-001", GoalID: g.ID, BindingType: GoalBindingServiceEndpoint, SourceRef: "postgres:5432", TargetRef: "env.DATABASE_URL", Status: GoalBindingPending}
	b2 := &GoalBinding{ID: "bind-002", GoalID: g.ID, BindingType: GoalBindingNetworkExposure, SourceRef: "nginx:80", TargetRef: "app:8080", Status: GoalBindingPending}

	if err := gs.AppendGoalBinding(ctx, b1, bus); err != nil {
		t.Fatalf("append binding 1: %v", err)
	}
	if err := gs.AppendGoalBinding(ctx, b2, bus); err != nil {
		t.Fatalf("append binding 2: %v", err)
	}

	bindings, err := gs.ListGoalBindings(ctx, g.ID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}
	if bindings[0].SourceRef != "postgres:5432" {
		t.Errorf("first binding source_ref: got %q, want postgres:5432", bindings[0].SourceRef)
	}
}

func TestGoalBinding_Resolve_EmitsEvent(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-resolve-test", Description: "resolve test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	b := &GoalBinding{ID: "bind-resolve-1", GoalID: g.ID, BindingType: GoalBindingServiceEndpoint, SourceRef: "postgres:5432", TargetRef: "env.DATABASE_URL", Status: GoalBindingPending}
	if err := gs.AppendGoalBinding(ctx, b, bus); err != nil {
		t.Fatalf("append binding: %v", err)
	}

	ch, unsub := bus.Subscribe(8)
	defer unsub()

	var gotEvent eventing.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if ev.Type == EventGoalBindingResolved {
				gotEvent = ev
				return
			}
		}
	}()

	resolvedValue := "postgres://user:pass@localhost:5432/db"
	if err := gs.ResolveGoalBinding(ctx, b.ID, resolvedValue, bus); err != nil {
		t.Fatalf("resolve binding: %v", err)
	}
	<-done

	if gotEvent.Type != EventGoalBindingResolved {
		t.Errorf("expected event %q", EventGoalBindingResolved)
	}

	bindings, _ := gs.ListGoalBindings(ctx, g.ID)
	if bindings[0].Status != GoalBindingResolved {
		t.Errorf("expected resolved status, got %q", bindings[0].Status)
	}
	if bindings[0].ResolvedValue != resolvedValue {
		t.Errorf("expected resolved_value %q, got %q", resolvedValue, bindings[0].ResolvedValue)
	}
}

func TestGoalStore_CreateGoalFull(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{
		ID:          "goal-full-1",
		Description: "Full declarative goal",
		Status:      GoalCreated,
		Packages:    []string{"postgres"},
		SandboxIDs:  []string{},
	}
	steps := []*GoalStep{
		{ID: "step-f1", GoalID: g.ID, Primitive: "fs.write", Input: json.RawMessage(`{"path":"a"}`), Status: GoalStepPending},
		{ID: "step-f2", GoalID: g.ID, Primitive: "shell.exec", Input: json.RawMessage(`{"command":"ls"}`), Status: GoalStepPending},
	}

	if err := gs.CreateGoalFull(ctx, g, steps, bus); err != nil {
		t.Fatalf("create goal full: %v", err)
	}

	got, found, err := gs.GetGoal(ctx, g.ID)
	if err != nil || !found {
		t.Fatalf("get goal: %v, found=%v", err, found)
	}
	if got.Description != g.Description {
		t.Errorf("description mismatch: %q", got.Description)
	}

	gotSteps, err := gs.ListGoalSteps(ctx, g.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(gotSteps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(gotSteps))
	}
	if gotSteps[0].Primitive != "fs.write" {
		t.Errorf("step[0] primitive: got %q", gotSteps[0].Primitive)
	}
	if gotSteps[1].Seq != 2 {
		t.Errorf("step[1] seq: got %d, want 2", gotSteps[1].Seq)
	}
}

func TestGoalStore_CreateGoalFull_NoSteps(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-full-empty", Description: "No steps", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoalFull(ctx, g, nil, bus); err != nil {
		t.Fatalf("create goal full with no steps: %v", err)
	}
	steps, _ := gs.ListGoalSteps(ctx, g.ID)
	if len(steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(steps))
	}
}

func TestGoalStore_NewStatuses(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-newstatus", Description: "new status test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	if err := gs.UpdateGoalStatus(ctx, g.ID, GoalPaused, bus); err != nil {
		t.Fatalf("update to paused: %v", err)
	}
	got, _, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != GoalPaused {
		t.Errorf("expected paused, got %q", got.Status)
	}

	step := &GoalStep{ID: "step-newstatus", GoalID: g.ID, Primitive: "fs.write", Input: json.RawMessage(`{}`), Status: GoalStepPending, Seq: 1}
	if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
		t.Fatalf("append step: %v", err)
	}
	if err := gs.UpdateGoalStepStatus(ctx, step.ID, GoalStepRolledBack, nil, bus); err != nil {
		t.Fatalf("update step to rolled_back: %v", err)
	}
	gotSteps, _ := gs.ListGoalSteps(ctx, g.ID)
	if gotSteps[0].Status != GoalStepRolledBack {
		t.Errorf("expected rolled_back, got %q", gotSteps[0].Status)
	}
}

func TestGoalStore_VerificationWithCheckType(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-checktype", Description: "check type test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	v := &GoalVerification{
		ID:          "verify-ct-1",
		GoalID:      g.ID,
		Status:      GoalVerificationPending,
		CheckType:   "primitive_call",
		CheckParams: json.RawMessage(`{"method":"verify.test","params":{"command":"make test"}}`),
	}
	if err := gs.AppendGoalVerification(ctx, v, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	verifications, err := gs.ListGoalVerifications(ctx, g.ID)
	if err != nil {
		t.Fatalf("list verifications: %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("expected 1, got %d", len(verifications))
	}
	if verifications[0].CheckType != "primitive_call" {
		t.Errorf("check_type: got %q, want primitive_call", verifications[0].CheckType)
	}
	if string(verifications[0].CheckParams) != `{"method":"verify.test","params":{"command":"make test"}}` {
		t.Errorf("check_params: got %s", verifications[0].CheckParams)
	}
}

func TestGoalStore_UpdateGoalVerification_PersistsEvidenceAndGoalID(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-verify-update", Description: "verify update", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	v := &GoalVerification{
		ID:          "verify-update-1",
		GoalID:      g.ID,
		Status:      GoalVerificationPending,
		CheckType:   "http_probe",
		CheckParams: json.RawMessage(`{"url":"http://example.com"}`),
	}
	if err := gs.AppendGoalVerification(ctx, v, bus); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	ch, unsub := bus.Subscribe(8)
	defer unsub()

	done := make(chan eventing.Event, 1)
	go func() {
		for ev := range ch {
			if ev.Type == EventGoalVerificationStarted {
				done <- ev
				return
			}
		}
	}()

	evidence := json.RawMessage(`{"observed_status":200}`)
	if err := gs.UpdateGoalVerification(ctx, v.ID, GoalVerificationRunning, "running http probe", evidence, bus); err != nil {
		t.Fatalf("update verification: %v", err)
	}

	updated, err := gs.ListGoalVerifications(ctx, g.ID)
	if err != nil {
		t.Fatalf("list verifications: %v", err)
	}
	if updated[0].Status != GoalVerificationRunning {
		t.Fatalf("status: got %q, want running", updated[0].Status)
	}
	if string(updated[0].Evidence) != string(evidence) {
		t.Fatalf("evidence: got %s, want %s", updated[0].Evidence, evidence)
	}

	ev := <-done
	var payload map[string]any
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload["goal_id"] != g.ID {
		t.Fatalf("goal_id: got %v, want %s", payload["goal_id"], g.ID)
	}
	if payload["status"] != string(GoalVerificationRunning) {
		t.Fatalf("status payload: got %v", payload["status"])
	}
}

func TestGoalBinding_Fail(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-fail-bind-test", Description: "fail binding test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	b := &GoalBinding{ID: "bind-fail-1", GoalID: g.ID, BindingType: GoalBindingCredential, SourceRef: "app", TargetRef: "db-secret", Status: GoalBindingPending}
	if err := gs.AppendGoalBinding(ctx, b, bus); err != nil {
		t.Fatalf("append binding: %v", err)
	}
	if err := gs.FailGoalBinding(ctx, b.ID, "secret not found", bus); err != nil {
		t.Fatalf("fail binding: %v", err)
	}
	bindings, _ := gs.ListGoalBindings(ctx, g.ID)
	if bindings[0].Status != GoalBindingFailed {
		t.Errorf("expected failed status, got %q", bindings[0].Status)
	}
	if bindings[0].FailureReason != "secret not found" {
		t.Errorf("expected failure_reason, got %q", bindings[0].FailureReason)
	}
}

// ── Review tests ──────────────────────────────────────────────────────────────

func TestGoalReview_CreateAndList(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-rev-1", Description: "review test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	if err := gs.CreateGoal(ctx, g, bus); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	step := &GoalStep{ID: "step-rev-1", GoalID: g.ID, Primitive: "shell.exec", Status: GoalStepPending, RiskLevel: "high", Reversible: false, Seq: 1}
	if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
		t.Fatalf("append step: %v", err)
	}

	rev := &GoalReview{
		ID:         "rev-1",
		GoalID:     g.ID,
		StepID:     step.ID,
		Status:     GoalReviewPending,
		Primitive:  "shell.exec",
		RiskLevel:  "high",
		Reversible: false,
	}
	if err := gs.CreateGoalReview(ctx, rev, bus); err != nil {
		t.Fatalf("create review: %v", err)
	}

	reviews, err := gs.ListGoalReviews(ctx, g.ID)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	if reviews[0].Status != GoalReviewPending {
		t.Errorf("status: got %q, want pending", reviews[0].Status)
	}
	if reviews[0].RiskLevel != "high" {
		t.Errorf("risk_level: got %q, want high", reviews[0].RiskLevel)
	}
	if reviews[0].Reversible != false {
		t.Errorf("reversible: got %v, want false", reviews[0].Reversible)
	}
}

func TestGoalReview_Approve(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-rev-approve", Description: "approve test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	_ = gs.CreateGoal(ctx, g, bus)

	rev := &GoalReview{ID: "rev-approve-1", GoalID: g.ID, StepID: "step-a", Status: GoalReviewPending, Primitive: "fs.write", RiskLevel: "high", Reversible: true}
	_ = gs.CreateGoalReview(ctx, rev, bus)

	if err := gs.DecideGoalReview(ctx, rev.ID, GoalReviewApproved, "LGTM", bus); err != nil {
		t.Fatalf("approve review: %v", err)
	}

	got, found, err := gs.GetGoalReview(ctx, rev.ID)
	if err != nil || !found {
		t.Fatalf("get review: %v found=%v", err, found)
	}
	if got.Status != GoalReviewApproved {
		t.Errorf("status: got %q, want approved", got.Status)
	}
	if got.DecisionReason != "LGTM" {
		t.Errorf("decision_reason: got %q", got.DecisionReason)
	}
}

func TestGoalReview_IdempotentApprove(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-rev-idem", Description: "idem test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	_ = gs.CreateGoal(ctx, g, bus)

	rev := &GoalReview{ID: "rev-idem-1", GoalID: g.ID, StepID: "step-b", Status: GoalReviewPending, Primitive: "fs.write", RiskLevel: "high", Reversible: true}
	_ = gs.CreateGoalReview(ctx, rev, bus)
	_ = gs.DecideGoalReview(ctx, rev.ID, GoalReviewApproved, "", bus)

	// Second approve on already-approved → nil (no-op).
	if err := gs.DecideGoalReview(ctx, rev.ID, GoalReviewApproved, "", bus); err != nil {
		t.Errorf("re-approve should be no-op, got: %v", err)
	}
}

func TestGoalReview_ConflictingDecision(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-rev-conflict", Description: "conflict test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	_ = gs.CreateGoal(ctx, g, bus)

	rev := &GoalReview{ID: "rev-conflict-1", GoalID: g.ID, StepID: "step-c", Status: GoalReviewPending, Primitive: "fs.delete", RiskLevel: "high", Reversible: false}
	_ = gs.CreateGoalReview(ctx, rev, bus)
	_ = gs.DecideGoalReview(ctx, rev.ID, GoalReviewApproved, "", bus)

	// Now try to reject the already-approved review → ErrReviewConflict.
	err := gs.DecideGoalReview(ctx, rev.ID, GoalReviewRejected, "too late", bus)
	if err == nil {
		t.Fatal("expected ErrReviewConflict, got nil")
	}
	if !errors.Is(err, ErrReviewConflict) {
		t.Errorf("expected ErrReviewConflict, got: %v", err)
	}
}

func TestGoalStep_RiskLevelRoundTrip(t *testing.T) {
	t.Parallel()
	gs, bus := openTestGoalStore(t)
	ctx := context.Background()

	g := &Goal{ID: "goal-risk-1", Description: "risk test", Status: GoalCreated, Packages: []string{}, SandboxIDs: []string{}}
	_ = gs.CreateGoal(ctx, g, bus)

	step := &GoalStep{
		ID:         "step-risk-1",
		GoalID:     g.ID,
		Primitive:  "shell.exec",
		Input:      json.RawMessage(`{"command":"rm -rf /"}`),
		Status:     GoalStepPending,
		RiskLevel:  "high",
		Reversible: false,
		Seq:        1,
	}
	if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
		t.Fatalf("append step: %v", err)
	}

	steps, err := gs.ListGoalSteps(ctx, g.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].RiskLevel != "high" {
		t.Errorf("risk_level: got %q, want high", steps[0].RiskLevel)
	}
	if steps[0].Reversible != false {
		t.Errorf("reversible: got %v, want false", steps[0].Reversible)
	}
}
