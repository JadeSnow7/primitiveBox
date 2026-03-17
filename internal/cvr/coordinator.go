package cvr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type CVRResult struct {
	CheckpointID   string
	Passed         bool
	StrategyResult StrategyResult
	AppliedAction  RecoveryAction
	TraceRef       string
	LayerAOutcome  string
}

type CVRRequest struct {
	PrimitiveID string
	Intent      PrimitiveIntent
	Params      any
	Exec        StrategyExecutor
	TraceID     string
	StepID      string
	Attempt     int
	CVRDepth    int
}

type CVRCoordinator struct {
	store    CheckpointManifestStore
	strategy VerifyStrategy
	tree     *DecisionTree
}

func NewCVRCoordinator(store CheckpointManifestStore, strategy VerifyStrategy, tree *DecisionTree) *CVRCoordinator {
	if tree == nil {
		tree = NewDefaultDecisionTree()
	}
	return &CVRCoordinator{
		store:    store,
		strategy: strategy,
		tree:     tree,
	}
}

func (c *CVRCoordinator) Execute(ctx context.Context, req CVRRequest) (CVRResult, error) {
	if req.CVRDepth >= MaxCVRDepth {
		return CVRResult{}, ErrCVRDepthExceeded
	}
	if req.Exec == nil {
		return CVRResult{}, errors.New("cvr coordinator requires executor")
	}

	result := CVRResult{}
	var manifest CheckpointManifest

	if shouldCheckpoint(req.Intent) {
		if c.store == nil {
			result.LayerAOutcome = "failed"
			return result, &LayerAErr{Cause: errors.New("checkpoint manifest store is nil")}
		}

		checkpointResult, checkpointErr := req.Exec.Execute(ctx, "state.checkpoint", map[string]any{
			"label": fmt.Sprintf("%s-%s", req.PrimitiveID, req.StepID),
		})
		if checkpointErr != nil {
			result.LayerAOutcome = "failed"
			return result, &LayerAErr{Cause: checkpointErr}
		}
		checkpointID, err := extractCheckpointID(checkpointResult)
		if err != nil {
			result.LayerAOutcome = "failed"
			return result, &LayerAErr{Cause: err}
		}

		manifest = CheckpointManifest{
			ID:               checkpointID,
			CheckpointID:     checkpointID,
			SandboxID:        "",
			PrimitiveID:      req.PrimitiveID,
			Intent:           req.Intent,
			Trigger:          TriggerIntentPolicy,
			CreatedAt:        time.Now().UTC(),
			StateRef:         "",
			TriggerPrimitive: req.PrimitiveID,
			TriggerReason:    checkpointReasonForIntent(req.Intent),
			TraceID:          req.TraceID,
			StepID:           req.StepID,
			Attempt:          req.Attempt,
			WorkspaceRoot:    "",
		}
		if err := c.store.SaveManifest(ctx, manifest); err != nil {
			result.LayerAOutcome = "failed"
			return result, &LayerAErr{Cause: err}
		}
		result.CheckpointID = manifest.CheckpointID
		result.LayerAOutcome = "checkpoint_created"
	} else {
		result.LayerAOutcome = "skipped"
	}

	execResult, execErr := req.Exec.Execute(ctx, req.PrimitiveID, req.Params)
	strategyResult := strategyResultFromExecution(execResult, execErr)
	if c.strategy != nil {
		runResult, err := c.strategy.Run(ctx, req.Exec, execResult, &manifest)
		if err != nil {
			runResult = StrategyResult{
				Outcome:     VerifyOutcomeError,
				Message:     err.Error(),
				RecoverHint: RecoverHintRetry,
			}
		}
		strategyResult = runResult
	}
	result.StrategyResult = strategyResult

	if strategyResult.Outcome != VerifyOutcomePassed {
		result.AppliedAction = c.tree.Decide(RecoveryCtx{
			FailureKind:    failureKindFromExecution(execErr, execResult, strategyResult),
			Attempt:        req.Attempt,
			Intent:         req.Intent,
			StrategyResult: strategyResult,
			MaxRetries:     3,
		})
	}
	result.Passed = strategyResult.Outcome == VerifyOutcomePassed

	if execErr != nil {
		return result, execErr
	}
	return result, nil
}

func shouldCheckpoint(intent PrimitiveIntent) bool {
	if intent.Category != IntentMutation {
		return false
	}
	if !intent.Reversible {
		return true
	}
	return intent.RiskLevel == RiskHigh
}

func checkpointReasonForIntent(intent PrimitiveIntent) CheckpointReason {
	if intent.Category == IntentMutation {
		return CheckpointReasonPreEdit
	}
	return CheckpointReasonPreExec
}

func strategyResultFromExecution(execResult ExecuteResult, execErr error) StrategyResult {
	switch {
	case errors.Is(execErr, context.DeadlineExceeded):
		return StrategyResult{
			Outcome:     VerifyOutcomeTimeout,
			Message:     execErr.Error(),
			RecoverHint: RecoverHintRetry,
		}
	case execErr != nil:
		return StrategyResult{
			Outcome:     VerifyOutcomeError,
			Message:     execErr.Error(),
			RecoverHint: RecoverHintRetry,
		}
	case execResult.Success:
		return StrategyResult{
			Outcome:     VerifyOutcomePassed,
			Message:     "execution passed",
			RecoverHint: RecoverHintSkip,
		}
	default:
		msg := execResult.ErrMsg
		if msg == "" {
			msg = "execution reported failure"
		}
		return StrategyResult{
			Outcome:     VerifyOutcomeFailed,
			Message:     msg,
			RecoverHint: RecoverHintRetry,
		}
	}
}

func failureKindFromExecution(execErr error, execResult ExecuteResult, strategyResult StrategyResult) FailureKind {
	switch strategyResult.Outcome {
	case VerifyOutcomeTimeout:
		return FailureKindTimeout
	case VerifyOutcomeFailed:
		return FailureKindVerifyFail
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		return FailureKindTimeout
	}
	if execErr != nil || !execResult.Success {
		return FailureKindExecError
	}
	return FailureKindVerifyFail
}

func extractCheckpointID(result ExecuteResult) (string, error) {
	if len(result.Data) == 0 {
		return "", errors.New("checkpoint result missing data")
	}

	if raw, ok := result.Data["checkpoint_id"]; ok {
		var checkpointID string
		if err := json.Unmarshal(raw, &checkpointID); err != nil {
			return "", fmt.Errorf("decode checkpoint id: %w", err)
		}
		if checkpointID != "" {
			return checkpointID, nil
		}
	}

	if raw, ok := result.Data["result"]; ok {
		var payload struct {
			CheckpointID string `json:"checkpoint_id"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", fmt.Errorf("decode checkpoint payload: %w", err)
		}
		if payload.CheckpointID != "" {
			return payload.CheckpointID, nil
		}
	}

	return "", errors.New("checkpoint result missing checkpoint_id")
}
