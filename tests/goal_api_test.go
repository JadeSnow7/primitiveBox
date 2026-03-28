package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/goal"
	"primitivebox/internal/orchestrator"
	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
)

func newGoalTestServer(t *testing.T) (http.Handler, *control.SQLiteGoalStore) {
	t.Helper()
	dbPath := t.TempDir() + "/goal_api_test.db"
	store, err := control.OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	bus := eventing.NewBus(store)
	gs := control.NewSQLiteGoalStore(store.DB())

	registry := primitive.NewRegistry()
	srv := rpc.NewServer(registry, nil, nil)
	srv.AttachEventing(bus, store)
	srv.AttachGoalStore(gs)

	return srv.Handler(), gs
}

// newGoalTestServerWithCoordinator wires a GoalCoordinator backed by a
// passthrough executor so execute/replay endpoints work in tests.
func newGoalTestServerWithCoordinator(t *testing.T) (http.Handler, *control.SQLiteGoalStore) {
	t.Helper()
	dbPath := t.TempDir() + "/goal_coord_test.db"
	store, err := control.OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	bus := eventing.NewBus(store)
	gs := control.NewSQLiteGoalStore(store.DB())

	registry := primitive.NewRegistry()
	srv := rpc.NewServer(registry, nil, nil)
	srv.AttachEventing(bus, store)
	srv.AttachGoalStore(gs)

	// Wire coordinator with a success executor so execution completes quickly.
	engine := orchestrator.NewEngine(&alwaysSuccessExecutor{})
	coord := goal.NewGoalCoordinator(gs, engine, bus, nil)
	srv.AttachGoalCoordinator(coord)

	return srv.Handler(), gs
}

func apiGet(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func apiPost(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestGoalAPI_CreateAndList(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	resp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Deploy postgres + app",
		"packages":    []string{"postgres", "myapp"},
		"sandbox_ids": []string{"sb-abc"},
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created["description"] != "Deploy postgres + app" {
		t.Errorf("description: got %v", created["description"])
	}
	if created["status"] != "created" {
		t.Errorf("status: got %v", created["status"])
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	listResp := apiGet(t, handler, "/api/v1/goals")
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listResp.Code)
	}
	var list map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	goals, _ := list["goals"].([]any)
	if len(goals) != 1 {
		t.Errorf("expected 1 goal in list, got %d", len(goals))
	}
}

func TestGoalAPI_GetDetail(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Detail test goal",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", createResp.Code)
	}
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", detailResp.Code, detailResp.Body.String())
	}
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)

	if _, ok := detail["steps"]; !ok {
		t.Error("expected steps field in detail response")
	}
	if _, ok := detail["verifications"]; !ok {
		t.Error("expected verifications field in detail response")
	}
	if _, ok := detail["bindings"]; !ok {
		t.Error("expected bindings field in detail response")
	}
}

func TestGoalAPI_GetNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	resp := apiGet(t, handler, "/api/v1/goals/nonexistent-id")
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

func TestGoalAPI_Replay(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Replay test goal",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	replayResp := apiPost(t, handler, "/api/v1/goals/"+id+"/replay", map[string]any{"mode": "full"})
	if replayResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", replayResp.Code, replayResp.Body.String())
	}
	var replay map[string]any
	json.NewDecoder(replayResp.Body).Decode(&replay)
	if replay["goal_id"] != id {
		t.Errorf("goal_id: got %v, want %v", replay["goal_id"], id)
	}
	if _, ok := replay["entries"]; !ok {
		t.Error("expected entries field in replay response")
	}
}

func TestGoalAPI_ReplayNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	resp := apiPost(t, handler, "/api/v1/goals/bad-id/replay", map[string]any{})
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

func TestGoalAPI_CreateBadJSON(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/goals", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGoalBindingsAPI_EmptyList(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "bindings test",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	resp := apiGet(t, handler, "/api/v1/goals/"+id+"/bindings")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	bindings, _ := result["bindings"].([]any)
	if len(bindings) != 0 {
		t.Errorf("expected empty bindings, got %d", len(bindings))
	}
}

func TestGoalBindingsAPI_GoalNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t)

	resp := apiGet(t, handler, "/api/v1/goals/bad-id/bindings")
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

func TestGoalAPI_CreateWithSteps(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServer(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Declarative goal with steps",
		"packages":    []string{"postgres"},
		"sandbox_ids": []string{},
		"steps": []map[string]any{
			{"primitive": "fs.write", "input": map[string]any{"path": "a.txt", "content": "x"}},
			{"primitive": "shell.exec", "input": map[string]any{"command": "ls"}},
		},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createResp.Code, createResp.Body.String())
	}
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Verify steps were persisted by fetching the detail.
	ctx := context.Background()
	steps, err := gs.ListGoalSteps(ctx, id)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Primitive != "fs.write" {
		t.Errorf("step[0] primitive: got %q", steps[0].Primitive)
	}
	if steps[1].Primitive != "shell.exec" {
		t.Errorf("step[1] primitive: got %q", steps[1].Primitive)
	}
}

func TestGoalAPI_Execute_NotFound(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServerWithCoordinator(t)

	resp := apiPost(t, handler, "/api/v1/goals/bad-id/execute", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

func TestGoalAPI_Execute_Accepted(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	// Create a goal with no steps so execution completes immediately.
	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Execute test",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", createResp.Code)
	}
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	execResp := apiPost(t, handler, "/api/v1/goals/"+id+"/execute", nil)
	if execResp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", execResp.Code, execResp.Body.String())
	}
	var execResult map[string]any
	json.NewDecoder(execResp.Body).Decode(&execResult)
	if execResult["goal_id"] != id {
		t.Errorf("goal_id: got %v, want %v", execResult["goal_id"], id)
	}
	if execResult["status"] != "executing" {
		t.Errorf("status: got %v, want executing", execResult["status"])
	}

	// Wait for the background execution to complete (no steps → should be fast).
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g, _, _ := gs.GetGoal(ctx, id)
		if g != nil && (g.Status == control.GoalCompleted || g.Status == control.GoalFailed || g.Status == control.GoalPaused) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	g, _, _ := gs.GetGoal(ctx, id)
	if g.Status != control.GoalCompleted {
		t.Errorf("expected goal to complete, got %q", g.Status)
	}
}

func TestGoalAPI_EventStream_EmitsVerificationLifecycle(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Execute verification test",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", createResp.Code)
	}
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	ctx := context.Background()
	if err := gs.AppendGoalVerification(ctx, &control.GoalVerification{
		ID:          "verify-stream-1",
		GoalID:      id,
		Status:      control.GoalVerificationPending,
		CheckType:   "primitive_call",
		CheckParams: json.RawMessage(`{"method":"verify.ok","expect":{"path":"ok","operator":"eq","expected":true}}`),
	}, nil); err != nil {
		t.Fatalf("append verification: %v", err)
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(streamCtx)
	resp := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(resp, req)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)

	execResp := apiPost(t, handler, "/api/v1/goals/"+id+"/execute", nil)
	if execResp.Code != http.StatusAccepted {
		t.Fatalf("execute failed: %d %s", execResp.Code, execResp.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g, _, _ := gs.GetGoal(ctx, id)
		if g != nil && g.Status == control.GoalCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	body := resp.Body.String()
	if !bytes.Contains([]byte(body), []byte("event: goal.verification_started")) {
		t.Fatalf("expected goal.verification_started in stream, got %s", body)
	}
	if !bytes.Contains([]byte(body), []byte("event: goal.verification_passed")) {
		t.Fatalf("expected goal.verification_passed in stream, got %s", body)
	}
}

func TestGoalAPI_Execute_AlreadyExecuting(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	// Create and manually set to executing.
	ctx := context.Background()
	g := &control.Goal{ID: "goal-already-exec-api", Description: "test", Status: control.GoalExecuting, Packages: []string{}, SandboxIDs: []string{}}
	bus := eventing.NewBus(nil)
	_ = gs.CreateGoal(ctx, g, bus)

	resp := apiPost(t, handler, "/api/v1/goals/goal-already-exec-api/execute", nil)
	if resp.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.Code)
	}
}

func TestGoalAPI_Execute_NoCoordinator(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServer(t) // no coordinator wired

	// Create a goal first.
	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "no coordinator test",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	resp := apiPost(t, handler, "/api/v1/goals/"+id+"/execute", nil)
	if resp.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", resp.Code)
	}
}

// ── Test executor ──────────────────────────────────────────────────────────────

// alwaysSuccessExecutor passes every primitive invocation.
type alwaysSuccessExecutor struct{}

func (a *alwaysSuccessExecutor) Execute(_ context.Context, method string, _ json.RawMessage) (*orchestrator.StepResult, error) {
	if method == "state.checkpoint" {
		data, _ := json.Marshal(map[string]any{"checkpoint_id": "cp-test"})
		return &orchestrator.StepResult{Success: true, Data: data}, nil
	}
	data, _ := json.Marshal(map[string]any{"ok": true})
	return &orchestrator.StepResult{Success: true, Data: data}, nil
}

func (a *alwaysSuccessExecutor) ListPrimitives() []string { return nil }

// ── Review / Resume API tests ──────────────────────────────────────────────────

// createGoalWithHighRiskStep creates a goal with one high-risk step, triggers
// execute (which should pause immediately), then returns the goal ID.
func createGoalWithHighRiskStep(t *testing.T, handler http.Handler, gs *control.SQLiteGoalStore) string {
	t.Helper()
	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "High-risk goal",
		"packages":    []string{},
		"sandbox_ids": []string{},
		"steps": []map[string]any{
			{"primitive": "shell.exec", "input": map[string]any{"command": "rm -rf /"}, "risk_level": "high", "reversible": false},
		},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create failed: %d: %s", createResp.Code, createResp.Body.String())
	}
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	execResp := apiPost(t, handler, "/api/v1/goals/"+id+"/execute", nil)
	if execResp.Code != http.StatusAccepted {
		t.Fatalf("execute failed: %d: %s", execResp.Code, execResp.Body.String())
	}

	// Wait for goal to reach paused state.
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		g, _, _ := gs.GetGoal(ctx, id)
		if g != nil && g.Status == control.GoalPaused {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	g, found, err := gs.GetGoal(ctx, id)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if !found || g == nil {
		t.Fatalf("expected goal %s to exist after execute", id)
	}
	if g.Status != control.GoalPaused {
		t.Fatalf("expected goal to be paused, got %q", g.Status)
	}
	return id
}

func TestGoalAPI_GetDetail_HasReviews(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", detailResp.Code, detailResp.Body.String())
	}
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)

	reviews, ok := detail["reviews"].([]any)
	if !ok {
		t.Fatal("expected reviews array in detail response")
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	review := reviews[0].(map[string]any)
	if review["status"] != "pending" {
		t.Errorf("review status: got %v, want pending", review["status"])
	}
	if review["risk_level"] != "high" {
		t.Errorf("review risk_level: got %v, want high", review["risk_level"])
	}
}

func TestGoalAPI_Approve_Success(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	// Get the review ID from detail.
	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	reviews := detail["reviews"].([]any)
	review := reviews[0].(map[string]any)
	reviewID := review["id"].(string)

	approveResp := apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{
		"review_id": reviewID,
	})
	if approveResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", approveResp.Code, approveResp.Body.String())
	}
	var result map[string]any
	json.NewDecoder(approveResp.Body).Decode(&result)
	if result["status"] != "approved" {
		t.Errorf("status: got %v, want approved", result["status"])
	}
	if result["review_id"] != reviewID {
		t.Errorf("review_id: got %v, want %v", result["review_id"], reviewID)
	}
}

func TestGoalAPI_Approve_Idempotent(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	reviewID := detail["reviews"].([]any)[0].(map[string]any)["id"].(string)

	// Approve twice — second call must also return 200.
	for i := 0; i < 2; i++ {
		resp := apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{"review_id": reviewID})
		if resp.Code != http.StatusOK {
			t.Fatalf("approve #%d: expected 200, got %d: %s", i+1, resp.Code, resp.Body.String())
		}
	}
}

func TestGoalAPI_Approve_ConflictAfterReject(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	reviewID := detail["reviews"].([]any)[0].(map[string]any)["id"].(string)

	// Reject first.
	rejectResp := apiPost(t, handler, "/api/v1/goals/"+id+"/reject", map[string]any{
		"review_id": reviewID,
		"reason":    "not safe",
	})
	if rejectResp.Code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d", rejectResp.Code)
	}

	// Approve after reject → 409.
	approveResp := apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{"review_id": reviewID})
	if approveResp.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", approveResp.Code)
	}
}

func TestGoalAPI_Approve_MissingReviewID(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	resp := apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.Code)
	}
}

func TestGoalAPI_Approve_ReviewNotFound(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	resp := apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{
		"review_id": "rev-does-not-exist",
	})
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}

func TestGoalAPI_Reject_Success(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	reviewID := detail["reviews"].([]any)[0].(map[string]any)["id"].(string)

	rejectResp := apiPost(t, handler, "/api/v1/goals/"+id+"/reject", map[string]any{
		"review_id": reviewID,
		"reason":    "too risky",
	})
	if rejectResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rejectResp.Code, rejectResp.Body.String())
	}
	var result map[string]any
	json.NewDecoder(rejectResp.Body).Decode(&result)
	if result["status"] != "rejected" {
		t.Errorf("status: got %v, want rejected", result["status"])
	}

	// Goal should be marked failed.
	ctx := context.Background()
	g, _, _ := gs.GetGoal(ctx, id)
	if g.Status != control.GoalFailed {
		t.Errorf("expected goal failed, got %q", g.Status)
	}
}

func TestGoalAPI_Resume_Success(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	// Get and approve the review.
	detailResp := apiGet(t, handler, "/api/v1/goals/"+id)
	var detail map[string]any
	json.NewDecoder(detailResp.Body).Decode(&detail)
	reviewID := detail["reviews"].([]any)[0].(map[string]any)["id"].(string)

	apiPost(t, handler, "/api/v1/goals/"+id+"/approve", map[string]any{"review_id": reviewID})

	// Resume.
	resumeResp := apiPost(t, handler, "/api/v1/goals/"+id+"/resume", nil)
	if resumeResp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resumeResp.Code, resumeResp.Body.String())
	}
	var resumeResult map[string]any
	json.NewDecoder(resumeResp.Body).Decode(&resumeResult)
	if resumeResult["status"] != "executing" {
		t.Errorf("status: got %v, want executing", resumeResult["status"])
	}

	// Wait for completion.
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g, _, _ := gs.GetGoal(ctx, id)
		if g != nil && (g.Status == control.GoalCompleted || g.Status == control.GoalFailed) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	g, _, _ := gs.GetGoal(ctx, id)
	if g.Status != control.GoalCompleted {
		t.Errorf("expected goal completed, got %q", g.Status)
	}
}

func TestGoalAPI_Resume_NotPaused(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	createResp := apiPost(t, handler, "/api/v1/goals", map[string]any{
		"description": "Resume not-paused test",
		"packages":    []string{},
		"sandbox_ids": []string{},
	})
	var created map[string]any
	json.NewDecoder(createResp.Body).Decode(&created)
	id := created["id"].(string)

	// Goal is in "created" status — resume should fail with 409.
	resumeResp := apiPost(t, handler, "/api/v1/goals/"+id+"/resume", nil)
	if resumeResp.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", resumeResp.Code, resumeResp.Body.String())
	}

	// Suppress unused gs warning.
	_ = gs
}

func TestGoalAPI_Resume_WithPendingReview(t *testing.T) {
	t.Parallel()
	handler, gs := newGoalTestServerWithCoordinator(t)

	id := createGoalWithHighRiskStep(t, handler, gs)

	// Resume without approving — still has a pending review → 409.
	resumeResp := apiPost(t, handler, "/api/v1/goals/"+id+"/resume", nil)
	if resumeResp.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", resumeResp.Code, resumeResp.Body.String())
	}
}

func TestGoalAPI_Resume_GoalNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := newGoalTestServerWithCoordinator(t)

	resp := apiPost(t, handler, "/api/v1/goals/bad-id/resume", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.Code)
	}
}
