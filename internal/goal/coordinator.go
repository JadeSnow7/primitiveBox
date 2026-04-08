package goal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/orchestrator"
	"primitivebox/internal/sandbox"
)

// errGoalPaused is returned by executeSteps when execution halts at a
// high-risk step that requires human review. The goal is already marked
// paused in the store before this error is returned.
var errGoalPaused = errors.New("goal paused for review")

// executeStepsOpts controls per-step execution behaviour.
type executeStepsOpts struct {
	// approvedStepIDs contains step IDs whose review has been approved, so the
	// coordinator should proceed with execution rather than creating a new review.
	approvedStepIDs map[string]bool
}

func newReviewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("rev-%s", hex.EncodeToString(b))
}

// GoalCoordinator bridges the goal control-plane (GoalStore) to the orchestrator
// Engine. It converts Goal+GoalSteps into orchestrator.Task, drives execution,
// syncs results back to the store, and implements real replay.
type GoalCoordinator struct {
	store              control.GoalStore
	engine             *orchestrator.Engine
	bus                *eventing.Bus
	verificationRunner *VerificationRunner
}

// NewGoalCoordinator creates a coordinator wired to the given store and engine.
func NewGoalCoordinator(store control.GoalStore, engine *orchestrator.Engine, bus *eventing.Bus, manager *sandbox.Manager) *GoalCoordinator {
	return &GoalCoordinator{
		store:              store,
		engine:             engine,
		bus:                bus,
		verificationRunner: NewVerificationRunner(store, engine, bus, manager),
	}
}

// Execute starts executing a goal. It is safe to call from a goroutine.
// Returns an error only if the goal cannot be loaded or is already in a
// terminal/active state; per-step errors are persisted to the store.
func (c *GoalCoordinator) Execute(ctx context.Context, goalID string) error {
	g, found, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return fmt.Errorf("get goal %s: %w", goalID, err)
	}
	if !found {
		return fmt.Errorf("goal %s not found", goalID)
	}
	switch g.Status {
	case control.GoalExecuting:
		return fmt.Errorf("goal %s is already executing", goalID)
	case control.GoalCompleted:
		return fmt.Errorf("goal %s is already completed", goalID)
	}

	if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalExecuting, c.bus); err != nil {
		return fmt.Errorf("set goal executing: %w", err)
	}

	steps, err := c.store.ListGoalSteps(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list steps for goal %s: %w", goalID, err)
	}

	runErr := c.executeSteps(ctx, goalID, steps, executeStepsOpts{approvedStepIDs: map[string]bool{}})
	if errors.Is(runErr, errGoalPaused) {
		return nil // goal already marked paused inside executeSteps
	}

	if runErr != nil {
		_ = c.store.UpdateGoalStatus(ctx, goalID, control.GoalFailed, c.bus)
		return runErr
	}

	return c.finalizeAfterExecution(ctx, goalID)
}

// Resume continues a paused goal from its first unfinished step. All pending
// reviews must be decided (no pending reviews) before resume is allowed.
func (c *GoalCoordinator) Resume(ctx context.Context, goalID string) error {
	g, found, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return fmt.Errorf("get goal %s: %w", goalID, err)
	}
	if !found {
		return fmt.Errorf("goal %s not found", goalID)
	}
	if g.Status != control.GoalPaused {
		return fmt.Errorf("goal %s is not paused (status: %s)", goalID, g.Status)
	}

	reviews, err := c.store.ListGoalReviews(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list reviews for goal %s: %w", goalID, err)
	}
	approvedStepIDs := map[string]bool{}
	for _, r := range reviews {
		if r.Status == control.GoalReviewPending {
			return fmt.Errorf("goal %s has a pending review", goalID)
		}
		if r.Status == control.GoalReviewApproved {
			approvedStepIDs[r.StepID] = true
		}
	}

	if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalExecuting, c.bus); err != nil {
		return fmt.Errorf("set goal executing: %w", err)
	}
	c.bus.Publish(ctx, eventing.Event{
		Type:    control.EventGoalResumed,
		Source:  "goal",
		Message: goalID,
		Data:    eventing.MustJSON(map[string]any{"goal_id": goalID}),
	})

	steps, err := c.store.ListGoalSteps(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list steps for goal %s: %w", goalID, err)
	}

	runErr := c.executeSteps(ctx, goalID, steps, executeStepsOpts{approvedStepIDs: approvedStepIDs})
	if errors.Is(runErr, errGoalPaused) {
		return nil // hit another high-risk step; already marked paused
	}

	if runErr != nil {
		_ = c.store.UpdateGoalStatus(ctx, goalID, control.GoalFailed, c.bus)
		return runErr
	}

	return c.finalizeAfterExecution(ctx, goalID)
}

// executeSteps runs steps in seq order via Engine.ExecuteStepViaCVR (full CVR
// path: pre-checkpoint → execute → verify → recover), honouring the review gate
// for high-risk steps. Steps already in a terminal state are skipped, enabling
// seamless resume after a pause.
func (c *GoalCoordinator) executeSteps(ctx context.Context, goalID string, steps []*control.GoalStep, opts executeStepsOpts) error {
	for _, step := range steps {
		switch step.Status {
		case control.GoalStepPassed, control.GoalStepSkipped, control.GoalStepRolledBack:
			continue
		case control.GoalStepFailed:
			return fmt.Errorf("step %s (%s) already failed", step.ID, step.Primitive)
		}

		// Check if this step needs a human review gate.
		if step.RiskLevel == "high" && !opts.approvedStepIDs[step.ID] {
			review := &control.GoalReview{
				ID:         newReviewID(),
				GoalID:     goalID,
				StepID:     step.ID,
				Status:     control.GoalReviewPending,
				Primitive:  step.Primitive,
				RiskLevel:  step.RiskLevel,
				Reversible: step.Reversible,
			}
			if createErr := c.store.CreateGoalReview(ctx, review, c.bus); createErr != nil {
				log.Printf("[GoalCoordinator] create review for step %s: %v", step.ID, createErr)
			}
			if stepErr := c.store.UpdateGoalStepStatus(ctx, step.ID, control.GoalStepAwaitingReview, nil, c.bus); stepErr != nil {
				log.Printf("[GoalCoordinator] mark step awaiting_review %s: %v", step.ID, stepErr)
			}
			if goalErr := c.store.UpdateGoalStatus(ctx, goalID, control.GoalPaused, c.bus); goalErr != nil {
				log.Printf("[GoalCoordinator] mark goal paused %s: %v", goalID, goalErr)
			}
			return errGoalPaused
		}

		// Mark step as running.
		if stepErr := c.store.UpdateGoalStepStatus(ctx, step.ID, control.GoalStepRunning, nil, c.bus); stepErr != nil {
			log.Printf("[GoalCoordinator] mark step running %s: %v", step.ID, stepErr)
		}

		// Execute the step primitive via the full CVR path:
		// Engine.ExecuteStepViaCVR → executeStepWithRecovery → CVRCoordinator.Execute
		// This ensures pre-checkpoint, verify, and recovery are applied for every
		// step with side effects. The human review gate (high-risk step approval) and
		// control-plane status sync are unchanged.
		// sandboxID is passed as "" because GoalStep does not carry a sandbox ID;
		// this is only used for trace/checkpoint manifest labelling.
		result, execErr := c.engine.ExecuteStepViaCVR(ctx, goalID, "", step.ID, step.Primitive, step.Input)
		if execErr != nil || (result != nil && !result.Success) {
			var out json.RawMessage
			if result != nil {
				out = result.Data
			}
			if stepErr := c.store.UpdateGoalStepStatus(ctx, step.ID, control.GoalStepFailed, out, c.bus); stepErr != nil {
				log.Printf("[GoalCoordinator] mark step failed %s: %v", step.ID, stepErr)
			}
			if execErr != nil {
				return fmt.Errorf("step %s (%s): %w", step.ID, step.Primitive, execErr)
			}
			return fmt.Errorf("step %s (%s) failed", step.ID, step.Primitive)
		}
		if stepErr := c.store.UpdateGoalStepStatus(ctx, step.ID, control.GoalStepPassed, result.Data, c.bus); stepErr != nil {
			log.Printf("[GoalCoordinator] mark step passed %s: %v", step.ID, stepErr)
		}
	}
	return nil
}

// Replay reconstructs the orchestrator.Task from stored step results and
// generates a GoalReplayResult using orchestrator.Replay.
func (c *GoalCoordinator) Replay(ctx context.Context, goalID string, mode orchestrator.ReplayMode) (*control.GoalReplayResult, error) {
	g, found, err := c.store.GetGoal(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("get goal %s: %w", goalID, err)
	}
	if !found {
		return nil, fmt.Errorf("goal %s not found", goalID)
	}

	steps, err := c.store.ListGoalSteps(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list steps for goal %s: %w", goalID, err)
	}

	// Reconstruct the task from stored state so Replay can traverse it.
	task := &orchestrator.Task{
		ID:          g.ID,
		Description: g.Description,
		Status:      orchestrator.TaskStatus(g.Status),
	}
	for _, s := range steps {
		step := orchestrator.Step{
			ID:           s.ID,
			Primitive:    s.Primitive,
			Input:        s.Input,
			CheckpointID: s.CheckpointID,
			Status:       goalStepStatusToOrchestrator(s.Status),
		}
		if len(s.Output) > 0 {
			step.Result = &orchestrator.StepResult{
				Success: s.Status == control.GoalStepPassed,
				Data:    s.Output,
			}
		}
		task.Steps = append(task.Steps, step)
	}

	entries := orchestrator.Replay(task, mode)
	result := &control.GoalReplayResult{
		GoalID:  goalID,
		Mode:    string(mode),
		Entries: make([]control.GoalReplayEntry, 0, len(entries)),
	}
	for _, e := range entries {
		stepID := ""
		if e.StepIndex > 0 && e.StepIndex <= len(steps) {
			stepID = steps[e.StepIndex-1].ID
		}
		result.Entries = append(result.Entries, control.GoalReplayEntry{
			Seq:          e.StepIndex,
			StepID:       stepID,
			Primitive:    e.Primitive,
			Input:        e.Input,
			Output:       e.Output,
			Status:       e.Status,
			CheckpointID: e.CheckpointID,
			Skipped:      e.Skipped,
		})
	}
	return result, nil
}

func (c *GoalCoordinator) finalizeAfterExecution(ctx context.Context, goalID string) error {
	verifications, err := c.store.ListGoalVerifications(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list verifications: %w", err)
	}
	if len(verifications) == 0 {
		if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalCompleted, c.bus); err != nil {
			return fmt.Errorf("set goal completed: %w", err)
		}
		return nil
	}

	if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalVerifying, c.bus); err != nil {
		return fmt.Errorf("set goal verifying: %w", err)
	}
	runResult, err := c.verificationRunner.Run(ctx, goalID)
	if err != nil {
		_ = c.store.UpdateGoalStatus(ctx, goalID, control.GoalFailed, c.bus)
		return err
	}
	if runResult.Failed {
		if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalFailed, c.bus); err != nil {
			return fmt.Errorf("set goal failed after verification: %w", err)
		}
		return fmt.Errorf("goal %s verification failed", goalID)
	}
	if err := c.store.UpdateGoalStatus(ctx, goalID, control.GoalCompleted, c.bus); err != nil {
		return fmt.Errorf("set goal completed: %w", err)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func orchestratorStepStatusToGoal(s orchestrator.StepStatus) control.GoalStepStatus {
	switch s {
	case orchestrator.StepPassed:
		return control.GoalStepPassed
	case orchestrator.StepFailed:
		return control.GoalStepFailed
	case orchestrator.StepSkipped:
		return control.GoalStepSkipped
	case orchestrator.StepRolledBack:
		return control.GoalStepRolledBack
	case orchestrator.StepRunning:
		return control.GoalStepRunning
	default:
		return control.GoalStepPending
	}
}

func goalStepStatusToOrchestrator(s control.GoalStepStatus) orchestrator.StepStatus {
	switch s {
	case control.GoalStepPassed:
		return orchestrator.StepPassed
	case control.GoalStepFailed:
		return orchestrator.StepFailed
	case control.GoalStepSkipped:
		return orchestrator.StepSkipped
	case control.GoalStepRolledBack:
		return orchestrator.StepRolledBack
	case control.GoalStepRunning:
		return orchestrator.StepRunning
	default:
		return orchestrator.StepPending
	}
}
