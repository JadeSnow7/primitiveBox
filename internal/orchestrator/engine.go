// Package orchestrator implements the task engine with plan-execute-verify-recover loop.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
	pbruntime "primitivebox/internal/runtime"
	"primitivebox/internal/runtrace"

	"github.com/google/uuid"
)

// --------------------------------------------------------------------------
// Engine: Task Execution Engine
// --------------------------------------------------------------------------

// Engine orchestrates plan → execute → verify → recover loops.
type Engine struct {
	executor      PrimitiveExecutor
	state         *StateTracker
	recovery      *RecoveryPolicy
	traceStore    runtrace.Store
	manifestStore cvr.CheckpointManifestStore
	cvrStrategy   cvr.VerifyStrategy
	cvrTree       *cvr.DecisionTree
	appRegistry   primitive.AppPrimitiveRegistry
}

// SetAppRegistry wires an AppPrimitiveRegistry so the engine can look up
// manifest Intent for app-registered primitives instead of defaulting to
// IntentMutation/RiskHigh for all unrecognised method names.
func (e *Engine) SetAppRegistry(reg primitive.AppPrimitiveRegistry) {
	e.appRegistry = reg
}

// NewEngine creates a new orchestrator engine.
func NewEngine(executor PrimitiveExecutor) *Engine {
	return NewEngineWithStores(executor, nil, nil)
}

// NewEngineWithStores creates a new orchestrator engine with optional CVR stores.
func NewEngineWithStores(executor PrimitiveExecutor, traceStore runtrace.Store, manifestStore cvr.CheckpointManifestStore) *Engine {
	return &Engine{
		executor:      executor,
		state:         NewStateTracker(),
		recovery:      NewRecoveryPolicy(),
		traceStore:    traceStore,
		manifestStore: manifestStore,
		cvrTree:       cvr.NewDefaultDecisionTree(),
	}
}

// RunTask executes a task through the full orchestration lifecycle.
func (e *Engine) RunTask(ctx context.Context, task *Task) error {
	task.Status = TaskExecuting
	task.UpdatedAt = time.Now()
	e.state.TrackTask(task)

	log.Printf("[Engine] Starting task %s: %s", task.ID, task.Description)

	for i, step := range task.Steps {
		log.Printf("[Engine] Step %d/%d: %s", i+1, len(task.Steps), step.Primitive)

		// Execute step with retry logic
		result, err := e.executeStepWithRecovery(ctx, task, &task.Steps[i])
		if err != nil {
			task.Status = TaskPaused
			task.UpdatedAt = time.Now()
			log.Printf("[Engine] Task %s paused: %v", task.ID, err)
			return fmt.Errorf("task paused at step %d: %w", i+1, err)
		}

		// Record step result
		task.Steps[i].Result = result
		task.Steps[i].Status = StepPassed
		task.Steps[i].CompletedAt = time.Now()
	}

	task.Status = TaskCompleted
	task.UpdatedAt = time.Now()
	log.Printf("[Engine] Task %s completed successfully", task.ID)
	return nil
}

// executeStepWithRecovery executes a step and applies recovery on failure.
func (e *Engine) executeStepWithRecovery(ctx context.Context, task *Task, step *Step) (*StepResult, error) {
	maxRetries := 3
	if task.MaxRetries > 0 {
		maxRetries = task.MaxRetries
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[Engine] Retry %d/%d for step %s", attempt, maxRetries, step.Primitive)
		}

		step.Status = StepRunning
		step.StartedAt = time.Now()
		intent := e.inferPrimitiveIntent(ctx, step.Primitive)
		intentJSON, _ := json.Marshal(intent)
		traceRecord := runtrace.StepRecord{
			TaskID:         task.ID,
			TraceID:        fmt.Sprintf("%s:%s", task.ID, step.ID),
			SessionID:      task.ID,
			AttemptID:      fmt.Sprintf("attempt-%d", attempt+1),
			SandboxID:      task.SandboxID,
			StepID:         step.ID,
			Primitive:      step.Primitive,
			IntentSnapshot: string(intentJSON),
			Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		}
		execAdapter := &cvrExecutorAdapter{executor: e.executor, intent: &intent}
		coordinator := cvr.NewCVRCoordinator(e.checkpointManifestStore(), e.cvrStrategy, e.cvrDecisionTree())
		cvrResult, err := coordinator.Execute(ctx, cvr.CVRRequest{
			PrimitiveID: step.Primitive,
			SandboxID:   task.SandboxID,
			Intent:      intent,
			Params:      step.Input,
			Exec:        execAdapter,
			TraceID:     traceRecord.TraceID,
			StepID:      step.ID,
			Attempt:     attempt,
			CVRDepth:    0,
		})
		duration := time.Since(step.StartedAt)
		step.Duration = duration
		result := execAdapter.lastResult
		traceRecord.DurationMs = duration.Milliseconds()
		traceRecord.CheckpointID = cvrResult.CheckpointID
		traceRecord.LayerAOutcome = cvrResult.LayerAOutcome
		traceRecord.RecoveryPath = string(cvrResult.AppliedAction)
		traceRecord.StrategyOutcome = string(cvrResult.StrategyResult.Outcome)
		traceRecord.VerifyResult = string(cvrResult.StrategyResult.Outcome)
		traceRecord.AffectedScopes = append([]string(nil), intent.AffectedScopes...)
		if e.cvrStrategy != nil {
			traceRecord.StrategyName = e.cvrStrategy.Name()
		}
		if errors.Is(err, cvr.ErrCVRDepthExceeded) {
			traceRecord.CVRDepthExceeded = true
		}
		if result != nil && result.Error != nil {
			traceRecord.FailureKind = strings.ToLower(result.Error.Kind.String())
		}
		if err != nil && traceRecord.FailureKind == "" {
			traceRecord.FailureKind = "exec_error"
		}
		step.CheckpointID = cvrResult.CheckpointID
		e.persistTraceRecord(ctx, traceRecord)
		if errors.Is(err, cvr.ErrCVRDepthExceeded) {
			step.Status = StepFailed
			return nil, err
		}

		if err == nil && cvrResult.Passed && result != nil && result.Success {
			return result, nil
		}

		// Classify failure
		lastErr = err
		if lastErr == nil && result != nil && result.Error != nil {
			lastErr = errors.New(result.Error.Message)
		}
		if lastErr == nil {
			lastErr = errors.New("cvr execution failed")
		}
		var failureKind FailureKind
		if result != nil && result.Error != nil {
			failureKind = result.Error.Kind
		} else {
			failureKind = FailureUnknown
		}

		// Apply recovery strategy from CVR first, falling back to legacy retry policy.
		action := ActionRetry
		switch cvrResult.AppliedAction {
		case cvr.RecoveryActionRetry:
			if attempt >= maxRetries {
				action = e.recovery.Decide(failureKind, attempt, maxRetries)
			} else {
				action = ActionRetry
			}
		case cvr.RecoveryActionRollback:
			// Attempt a real state restore using the checkpoint created before this step.
			if cvrResult.CheckpointID == "" {
				lastErr = fmt.Errorf("rollback requested but no checkpoint_id available for %s", step.Primitive)
				action = ActionFail
			} else {
				restoreParams, _ := json.Marshal(map[string]string{"checkpoint_id": cvrResult.CheckpointID})
				_, restoreErr := e.executor.Execute(ctx, "state.restore", restoreParams)
				if restoreErr != nil {
					lastErr = fmt.Errorf("rollback failed for checkpoint %s: %w", cvrResult.CheckpointID, restoreErr)
					action = ActionFail
				} else {
					log.Printf("[Engine] Rolled back to checkpoint %s for step %s", cvrResult.CheckpointID, step.Primitive)
					step.Status = StepRolledBack
					action = ActionPause
				}
			}
		case cvr.RecoveryActionEscalate, cvr.RecoveryActionRewrite:
			// Surface to caller for human or AI re-planning; stop retrying.
			step.Escalated = true
			action = ActionPause
		case cvr.RecoveryActionAbort:
			// Permanent failure — do not retry, do not pause for human.
			action = ActionFail
		case cvr.RecoveryActionSkip:
			// Skip this step and continue the task.
			action = ActionContinue
		case "":
			action = e.recovery.Decide(failureKind, attempt, maxRetries)
		default:
			action = e.recovery.Decide(failureKind, attempt, maxRetries)
		}
		log.Printf("[Engine] Failure kind=%s, action=%s", failureKind, action)

		switch action {
		case ActionRetry:
			continue
		case ActionPause:
			// Preserve StepRolledBack if rollback already set it.
			if step.Status != StepRolledBack {
				step.Status = StepFailed
			}
			return nil, fmt.Errorf("step %s paused: %v", step.Primitive, lastErr)
		case ActionFail:
			step.Status = StepFailed
			return nil, fmt.Errorf("terminal failure for %s: %v", step.Primitive, lastErr)
		case ActionContinue:
			step.Status = StepSkipped
			return &StepResult{Success: true}, nil
		}
	}

	return nil, fmt.Errorf("step %s failed after %d attempts: %v", step.Primitive, maxRetries, lastErr)
}

// CreateTask builds a new task from a description and step definitions.
func (e *Engine) CreateTask(description string, sandboxID string, steps []StepDef) *Task {
	task := &Task{
		ID:          "task-" + uuid.New().String()[:8],
		Description: description,
		SandboxID:   sandboxID,
		Status:      TaskPlanning,
		MaxRetries:  3,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	for _, sd := range steps {
		params, _ := json.Marshal(sd.Params)
		task.Steps = append(task.Steps, Step{
			ID:        "step-" + uuid.New().String()[:8],
			Primitive: sd.Primitive,
			Input:     params,
			Status:    StepPending,
		})
	}

	return task
}

// StepDef is a convenience type for defining task steps.
type StepDef struct {
	Primitive string
	Params    any
}

type cvrExecutorAdapter struct {
	executor   PrimitiveExecutor
	intent     *cvr.PrimitiveIntent
	lastResult *StepResult
}

func (a *cvrExecutorAdapter) Execute(ctx context.Context, method string, params any) (cvr.ExecuteResult, error) {
	if a.intent != nil {
		ctx = context.WithValue(ctx, pbruntime.IntentContextKey, a.intent)
	}
	rawParams, ok := params.(json.RawMessage)
	if !ok {
		switch typed := params.(type) {
		case []byte:
			rawParams = json.RawMessage(typed)
		default:
			data, err := json.Marshal(params)
			if err != nil {
				return cvr.ExecuteResult{}, err
			}
			rawParams = data
		}
	}
	result, err := a.executor.Execute(ctx, method, rawParams)
	a.lastResult = result
	if result == nil {
		return cvr.ExecuteResult{Success: err == nil}, err
	}
	execResult := cvr.ExecuteResult{
		Success: result.Success,
	}
	if len(result.Data) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(result.Data, &m); err == nil {
			execResult.Data = m
		}
	}
	if result.Error != nil {
		execResult.ErrMsg = result.Error.Message
	}
	return execResult, err
}

func (e *Engine) checkpointManifestStore() cvr.CheckpointManifestStore {
	if e.manifestStore != nil {
		return e.manifestStore
	}
	return noopManifestStore{}
}

func (e *Engine) cvrDecisionTree() *cvr.DecisionTree {
	if e.cvrTree != nil {
		return e.cvrTree
	}
	return cvr.NewDefaultDecisionTree()
}

func (e *Engine) persistTraceRecord(ctx context.Context, record runtrace.StepRecord) {
	if e.traceStore != nil {
		_ = e.traceStore.RecordTraceStep(ctx, record)
	}
}

type noopManifestStore struct{}

func (noopManifestStore) SaveManifest(ctx context.Context, m cvr.CheckpointManifest) error {
	return nil
}
func (noopManifestStore) GetManifest(ctx context.Context, checkpointID string) (*cvr.CheckpointManifest, error) {
	return nil, nil
}
func (noopManifestStore) GetManifestChain(ctx context.Context, checkpointID string, maxDepth int) ([]cvr.CheckpointManifest, error) {
	return nil, nil
}
func (noopManifestStore) MarkCorrupted(ctx context.Context, checkpointID string, reason string) error {
	return nil
}

// inferPrimitiveIntent resolves the CVR intent for a primitive method name.
// For system primitives it uses static prefix matching.
// For unrecognised names it consults the AppPrimitiveRegistry; if a manifest
// is found its Intent is used directly.  If no manifest is found either, the
// conservative default (mutation/irreversible/high) is used and a warning is
// logged so the caller knows they may be paying unnecessary checkpoint costs.
func (e *Engine) inferPrimitiveIntent(ctx context.Context, primitiveID string) cvr.PrimitiveIntent {
	switch {
	case strings.HasPrefix(primitiveID, "fs.read"),
		strings.HasPrefix(primitiveID, "fs.list"),
		strings.HasPrefix(primitiveID, "fs.diff"),
		strings.HasPrefix(primitiveID, "code.search"),
		strings.HasPrefix(primitiveID, "code.symbols"):
		return cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		}
	case strings.HasPrefix(primitiveID, "verify."),
		strings.HasPrefix(primitiveID, "test."),
		strings.HasPrefix(primitiveID, "repo.run_tests"):
		return cvr.PrimitiveIntent{
			Category:   cvr.IntentVerification,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		}
	case primitiveID == "state.restore":
		return cvr.PrimitiveIntent{
			Category:   cvr.IntentRollback,
			Reversible: true,
			RiskLevel:  cvr.RiskHigh,
		}
	case strings.HasPrefix(primitiveID, "fs.write"),
		strings.HasPrefix(primitiveID, "macro.safe_edit"),
		strings.HasPrefix(primitiveID, "repo.patch"),
		strings.HasPrefix(primitiveID, "shell.exec"):
		return cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		}
	}

	// Unknown system primitive — check the app registry before defaulting.
	if e.appRegistry != nil {
		manifest, err := e.appRegistry.Get(ctx, primitiveID)
		if err == nil && manifest != nil {
			return manifest.Intent
		}
	}

	log.Printf("[Engine] unknown primitive %q: using conservative default intent (mutation/irreversible/high)", primitiveID)
	return cvr.PrimitiveIntent{
		Category:   cvr.IntentMutation,
		Reversible: false,
		RiskLevel:  cvr.RiskHigh,
	}
}
