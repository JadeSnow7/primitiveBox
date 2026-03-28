package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"primitivebox/internal/control"
	"primitivebox/internal/orchestrator"
)

func generateID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// handleAPIGoals handles GET /api/v1/goals and POST /api/v1/goals.
func (s *Server) handleAPIGoals(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		http.Error(w, "goal store unavailable", http.StatusNotImplemented)
		return
	}
	switch r.Method {
	case http.MethodGet:
		goals, err := s.goalStore.ListGoals(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if goals == nil {
			goals = []*control.Goal{}
		}
		writeJSON(w, map[string]any{"goals": goals})
	case http.MethodPost:
		var req struct {
			Description string   `json:"description"`
			Packages    []string `json:"packages"`
			SandboxIDs  []string `json:"sandbox_ids"`
			Steps       []struct {
				Primitive  string          `json:"primitive"`
				Input      json.RawMessage `json:"input"`
				RiskLevel  string          `json:"risk_level"`
				Reversible bool            `json:"reversible"`
			} `json:"steps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Description == "" {
			http.Error(w, "description is required", http.StatusBadRequest)
			return
		}
		if req.Packages == nil {
			req.Packages = []string{}
		}
		if req.SandboxIDs == nil {
			req.SandboxIDs = []string{}
		}
		g := &control.Goal{
			ID:          generateID("goal"),
			Description: req.Description,
			Status:      control.GoalCreated,
			Packages:    req.Packages,
			SandboxIDs:  req.SandboxIDs,
		}
		var goalSteps []*control.GoalStep
		for i, sd := range req.Steps {
			input := sd.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			riskLevel := sd.RiskLevel
			if riskLevel == "" {
				riskLevel = "low"
			}
			goalSteps = append(goalSteps, &control.GoalStep{
				ID:         generateID("step"),
				GoalID:     g.ID,
				Primitive:  sd.Primitive,
				Input:      input,
				Status:     control.GoalStepPending,
				RiskLevel:  riskLevel,
				Reversible: sd.Reversible,
				Seq:        i + 1,
			})
		}
		if err := s.goalStore.CreateGoalFull(r.Context(), g, goalSteps, s.eventBus); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(goalDetailResponse(g, goalSteps, nil, nil, nil))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPIGoalDetail handles GET /api/v1/goals/{id},
// POST /api/v1/goals/{id}/replay, and GET /api/v1/goals/{id}/bindings.
func (s *Server) handleAPIGoalDetail(w http.ResponseWriter, r *http.Request, path string) {
	if s.goalStore == nil {
		http.Error(w, "goal store unavailable", http.StatusNotImplemented)
		return
	}
	parts := strings.SplitN(path, "/", 2)
	goalID := parts[0]
	if goalID == "" {
		http.NotFound(w, r)
		return
	}

	// Sub-resource routing.
	if len(parts) == 2 {
		switch parts[1] {
		case "replay":
			s.handleAPIGoalReplay(w, r, goalID)
		case "bindings":
			s.handleAPIGoalBindings(w, r, goalID)
		case "execute":
			s.handleAPIGoalExecute(w, r, goalID)
		case "approve":
			s.handleAPIGoalApprove(w, r, goalID)
		case "reject":
			s.handleAPIGoalReject(w, r, goalID)
		case "resume":
			s.handleAPIGoalResume(w, r, goalID)
		default:
			http.NotFound(w, r)
		}
		return
	}

	// GET /api/v1/goals/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	goal, found, err := s.goalStore.GetGoal(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}
	steps, err := s.goalStore.ListGoalSteps(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verifications, err := s.goalStore.ListGoalVerifications(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	bindings, err := s.goalStore.ListGoalBindings(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reviews, err := s.goalStore.ListGoalReviews(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, goalDetailResponse(goal, steps, verifications, bindings, reviews))
}

// handleAPIGoalReplay handles POST /api/v1/goals/{id}/replay.
// When a GoalCoordinator is available it uses real orchestrator replay;
// otherwise it falls back to the store-level stub.
func (s *Server) handleAPIGoalReplay(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Mode == "" {
		req.Mode = "full"
	}

	_, found, err := s.goalStore.GetGoal(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	// Use real coordinator replay when available.
	if s.goalCoordinator != nil {
		mode := orchestrator.ReplayMode(req.Mode)
		result, replayErr := s.goalCoordinator.Replay(r.Context(), goalID, mode)
		if replayErr != nil {
			http.Error(w, replayErr.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
		return
	}

	// Fallback: emit replay events and return step list.
	_ = s.goalStore.ReplayGoal(r.Context(), goalID, s.eventBus)
	steps, err := s.goalStore.ListGoalSteps(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entries := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		entry := map[string]any{
			"seq":           step.Seq,
			"step_id":       step.ID,
			"primitive":     step.Primitive,
			"input":         step.Input,
			"status":        step.Status,
			"checkpoint_id": step.CheckpointID,
			"skipped":       step.Status == control.GoalStepSkipped,
		}
		if step.Output != nil {
			entry["output"] = step.Output
		}
		entries = append(entries, entry)
	}
	writeJSON(w, map[string]any{
		"goal_id": goalID,
		"mode":    req.Mode,
		"entries": entries,
	})
}

// handleAPIGoalExecute handles POST /api/v1/goals/{id}/execute.
// It launches execution in the background and returns 202 Accepted immediately.
func (s *Server) handleAPIGoalExecute(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.goalCoordinator == nil {
		http.Error(w, "goal coordinator unavailable", http.StatusNotImplemented)
		return
	}

	g, found, err := s.goalStore.GetGoal(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}
	switch g.Status {
	case control.GoalExecuting:
		http.Error(w, "goal is already executing", http.StatusConflict)
		return
	case control.GoalCompleted:
		http.Error(w, "goal is already completed", http.StatusConflict)
		return
	}

	// Launch execution in the background; use a detached context so it
	// outlives the HTTP request.
	go func() {
		if execErr := s.goalCoordinator.Execute(context.Background(), goalID); execErr != nil {
			_ = execErr // status is persisted to the store
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"goal_id": goalID,
		"status":  string(control.GoalExecuting),
	})
}

// handleAPIGoalBindings handles GET /api/v1/goals/{id}/bindings.
func (s *Server) handleAPIGoalBindings(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, found, err := s.goalStore.GetGoal(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}
	bindings, err := s.goalStore.ListGoalBindings(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if bindings == nil {
		bindings = []*control.GoalBinding{}
	}
	writeJSON(w, map[string]any{"goal_id": goalID, "bindings": bindings})
}

// goalDetailResponse builds the full goal response shape.
func goalDetailResponse(g *control.Goal, steps []*control.GoalStep, verifications []*control.GoalVerification, bindings []*control.GoalBinding, reviews []*control.GoalReview) map[string]any {
	if steps == nil {
		steps = []*control.GoalStep{}
	}
	if verifications == nil {
		verifications = []*control.GoalVerification{}
	}
	if bindings == nil {
		bindings = []*control.GoalBinding{}
	}
	if reviews == nil {
		reviews = []*control.GoalReview{}
	}
	return map[string]any{
		"id":                 g.ID,
		"description":        g.Description,
		"status":             g.Status,
		"packages":           g.Packages,
		"sandbox_ids":        g.SandboxIDs,
		"steps":              steps,
		"verifications":      verifications,
		"verification_count": len(verifications),
		"bindings":           bindings,
		"reviews":            reviews,
		"created_at":         g.CreatedAt,
		"updated_at":         g.UpdatedAt,
	}
}

// handleAPIGoalApprove handles POST /api/v1/goals/{id}/approve.
func (s *Server) handleAPIGoalApprove(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ReviewID string `json:"review_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReviewID == "" {
		http.Error(w, "review_id is required", http.StatusBadRequest)
		return
	}

	// Verify the review belongs to this goal.
	review, found, err := s.goalStore.GetGoalReview(r.Context(), req.ReviewID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || review.GoalID != goalID {
		http.Error(w, "review not found", http.StatusNotFound)
		return
	}

	if err := s.goalStore.DecideGoalReview(r.Context(), req.ReviewID, control.GoalReviewApproved, "", s.eventBus); err != nil {
		if errors.Is(err, control.ErrReviewConflict) {
			http.Error(w, "conflicting review decision", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"goal_id":   goalID,
		"review_id": req.ReviewID,
		"status":    string(control.GoalReviewApproved),
	})
}

// handleAPIGoalReject handles POST /api/v1/goals/{id}/reject.
func (s *Server) handleAPIGoalReject(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ReviewID string `json:"review_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReviewID == "" {
		http.Error(w, "review_id is required", http.StatusBadRequest)
		return
	}

	review, found, err := s.goalStore.GetGoalReview(r.Context(), req.ReviewID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || review.GoalID != goalID {
		http.Error(w, "review not found", http.StatusNotFound)
		return
	}

	if err := s.goalStore.DecideGoalReview(r.Context(), req.ReviewID, control.GoalReviewRejected, req.Reason, s.eventBus); err != nil {
		if errors.Is(err, control.ErrReviewConflict) {
			http.Error(w, "conflicting review decision", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Reject terminates the goal.
	if setErr := s.goalStore.UpdateGoalStatus(r.Context(), goalID, control.GoalFailed, s.eventBus); setErr != nil {
		http.Error(w, setErr.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"goal_id":   goalID,
		"review_id": req.ReviewID,
		"status":    string(control.GoalReviewRejected),
	})
}

// handleAPIGoalResume handles POST /api/v1/goals/{id}/resume.
// Launches resume in background; returns 202 Accepted immediately.
func (s *Server) handleAPIGoalResume(w http.ResponseWriter, r *http.Request, goalID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.goalCoordinator == nil {
		http.Error(w, "goal coordinator unavailable", http.StatusNotImplemented)
		return
	}

	g, found, err := s.goalStore.GetGoal(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}
	if g.Status != control.GoalPaused {
		http.Error(w, fmt.Sprintf("goal is not paused (status: %s)", g.Status), http.StatusConflict)
		return
	}

	// Verify no pending reviews remain.
	reviews, err := s.goalStore.ListGoalReviews(r.Context(), goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, rv := range reviews {
		if rv.Status == control.GoalReviewPending {
			http.Error(w, "goal has a pending review", http.StatusConflict)
			return
		}
	}

	go func() {
		if resumeErr := s.goalCoordinator.Resume(context.Background(), goalID); resumeErr != nil {
			_ = resumeErr // status persisted to store
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"goal_id": goalID,
		"status":  string(control.GoalExecuting),
	})
}
