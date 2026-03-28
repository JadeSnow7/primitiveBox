package goal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"primitivebox/internal/control"
	"primitivebox/internal/orchestrator"
)

func TestVerificationRunner_HTTPProbe_PassAndFail(t *testing.T) {
	t.Parallel()

	passServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`healthy`))
	}))
	defer passServer.Close()

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer failServer.Close()

	gs, bus := openTestStore(t)
	ctx := context.Background()
	engine := orchestrator.NewEngine(&fakeExecutor{})
	runner := NewVerificationRunner(gs, engine, bus, nil)

	for _, tc := range []struct {
		name          string
		goalID        string
		url           string
		expected      control.GoalVerificationStatus
		expectedGoal  control.GoalStatus
		expectedProbe int
	}{
		{name: "pass", goalID: "goal-http-pass", url: passServer.URL, expected: control.GoalVerificationPassed, expectedGoal: control.GoalVerifying, expectedProbe: http.StatusCreated},
		{name: "fail", goalID: "goal-http-fail", url: failServer.URL, expected: control.GoalVerificationFailed, expectedGoal: control.GoalVerifying, expectedProbe: http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &control.Goal{ID: tc.goalID, Description: tc.name, Status: control.GoalVerifying, Packages: []string{}, SandboxIDs: []string{}}
			if err := gs.CreateGoal(ctx, g, bus); err != nil {
				t.Fatalf("create goal: %v", err)
			}
			v := &control.GoalVerification{
				ID:          tc.goalID + "-verify",
				GoalID:      tc.goalID,
				Status:      control.GoalVerificationPending,
				CheckType:   "http_probe",
				CheckParams: json.RawMessage(`{"url":"` + tc.url + `","expected_status":201,"body_contains":"healthy"}`),
			}
			if err := gs.AppendGoalVerification(ctx, v, bus); err != nil {
				t.Fatalf("append verification: %v", err)
			}

			result, err := runner.Run(ctx, tc.goalID)
			if err != nil {
				t.Fatalf("run verifications: %v", err)
			}
			if result.Count != 1 {
				t.Fatalf("count: got %d", result.Count)
			}
			verifications, err := gs.ListGoalVerifications(ctx, tc.goalID)
			if err != nil {
				t.Fatalf("list verifications: %v", err)
			}
			if verifications[0].Status != tc.expected {
				t.Fatalf("status: got %q, want %q", verifications[0].Status, tc.expected)
			}
		})
	}
}

func TestVerificationRunner_JSONAssert_PassAndFail(t *testing.T) {
	t.Parallel()

	gs, bus := openTestStore(t)
	ctx := context.Background()
	engine := orchestrator.NewEngine(&fakeExecutor{})
	runner := NewVerificationRunner(gs, engine, bus, nil)

	for _, tc := range []struct {
		name     string
		goalID   string
		operator string
		expected any
		status   control.GoalVerificationStatus
	}{
		{name: "pass", goalID: "goal-json-pass", operator: "eq", expected: true, status: control.GoalVerificationPassed},
		{name: "fail", goalID: "goal-json-fail", operator: "eq", expected: false, status: control.GoalVerificationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &control.Goal{ID: tc.goalID, Description: tc.name, Status: control.GoalVerifying, Packages: []string{}, SandboxIDs: []string{}}
			if err := gs.CreateGoal(ctx, g, bus); err != nil {
				t.Fatalf("create goal: %v", err)
			}
			step := &control.GoalStep{
				ID:        tc.goalID + "-step",
				GoalID:    tc.goalID,
				Primitive: "noop.success",
				Output:    json.RawMessage(`{"ok":true}`),
				Status:    control.GoalStepPassed,
				Seq:       1,
			}
			if err := gs.AppendGoalStep(ctx, step, bus); err != nil {
				t.Fatalf("append step: %v", err)
			}
			if err := gs.UpdateGoalStepStatus(ctx, step.ID, control.GoalStepPassed, json.RawMessage(`{"ok":true}`), bus); err != nil {
				t.Fatalf("persist step output: %v", err)
			}
			expectedJSON, _ := json.Marshal(tc.expected)
			v := &control.GoalVerification{
				ID:          tc.goalID + "-verify",
				GoalID:      tc.goalID,
				Status:      control.GoalVerificationPending,
				CheckType:   "json_assert",
				CheckParams: json.RawMessage(`{"source_step_id":"` + step.ID + `","path":"ok","operator":"` + tc.operator + `","expected":` + string(expectedJSON) + `}`),
			}
			if err := gs.AppendGoalVerification(ctx, v, bus); err != nil {
				t.Fatalf("append verification: %v", err)
			}

			if _, err := runner.Run(ctx, tc.goalID); err != nil {
				t.Fatalf("run verifications: %v", err)
			}
			verifications, err := gs.ListGoalVerifications(ctx, tc.goalID)
			if err != nil {
				t.Fatalf("list verifications: %v", err)
			}
			if verifications[0].Status != tc.status {
				t.Fatalf("status: got %q, want %q", verifications[0].Status, tc.status)
			}
		})
	}
}
