package rpc

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"primitivebox/internal/cvr"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtrace"
)

type traceStoreReader interface {
	ListTraceSteps(ctx context.Context, sandboxID string, limit int) ([]runtrace.StepRecord, error)
	GetTraceStep(ctx context.Context, sandboxID, stepID string) (*runtrace.StepRecord, error)
}

type traceEvent struct {
	ID               string               `json:"id"`
	SandboxID        string               `json:"sandbox_id"`
	TraceID          string               `json:"trace_id"`
	PrimitiveID      string               `json:"primitive_id"`
	Timestamp        string               `json:"timestamp"`
	DurationMs       int64                `json:"duration_ms"`
	Attempt          int                  `json:"attempt"`
	CheckpointID     string               `json:"checkpoint_id"`
	LayerAOutcome    string               `json:"layer_a_outcome"`
	StrategyName     string               `json:"strategy_name"`
	StrategyOutcome  string               `json:"strategy_outcome"`
	RecoveryPath     string               `json:"recovery_path"`
	CVRDepthExceeded bool                 `json:"cvr_depth_exceeded"`
	IntentSnapshot   *traceIntentSnapshot `json:"intent_snapshot"`
	AffectedScopes   []string             `json:"affected_scopes"`
}

type traceIntentSnapshot struct {
	Category       string   `json:"category"`
	Reversible     bool     `json:"reversible"`
	RiskLevel      string   `json:"risk_level"`
	AffectedScopes []string `json:"affected_scopes"`
}

type traceListResponse struct {
	Events []traceEvent `json:"events"`
}

type appPrimitiveListResponse struct {
	AppPrimitives []primitive.AppPrimitiveManifest `json:"app_primitives"`
}

func projectTraceEvent(record runtrace.StepRecord) traceEvent {
	return traceEvent{
		ID:               defaultString(record.StepID, fallbackTraceEventID(record)),
		SandboxID:        record.SandboxID,
		TraceID:          record.TraceID,
		PrimitiveID:      record.Primitive,
		Timestamp:        record.Timestamp,
		DurationMs:       record.DurationMs,
		Attempt:          inferAttempt(record.AttemptID),
		CheckpointID:     record.CheckpointID,
		LayerAOutcome:    record.LayerAOutcome,
		StrategyName:     record.StrategyName,
		StrategyOutcome:  record.StrategyOutcome,
		RecoveryPath:     record.RecoveryPath,
		CVRDepthExceeded: record.CVRDepthExceeded,
		IntentSnapshot:   parseIntentSnapshot(record.IntentSnapshot),
		AffectedScopes:   append([]string(nil), record.AffectedScopes...),
	}
}

func parseIntentSnapshot(raw string) *traceIntentSnapshot {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var intent cvr.PrimitiveIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return nil
	}
	return &traceIntentSnapshot{
		Category:       string(intent.Category),
		Reversible:     intent.Reversible,
		RiskLevel:      string(intent.RiskLevel),
		AffectedScopes: append([]string(nil), intent.AffectedScopes...),
	}
}

func inferAttempt(attemptID string) int {
	if attemptID == "" {
		return 1
	}
	if value, err := strconv.Atoi(attemptID); err == nil && value > 0 {
		return value
	}
	if idx := strings.LastIndexByte(attemptID, '-'); idx >= 0 && idx < len(attemptID)-1 {
		if value, err := strconv.Atoi(attemptID[idx+1:]); err == nil && value > 0 {
			return value
		}
	}
	return 1
}

func fallbackTraceEventID(record runtrace.StepRecord) string {
	if record.TraceID != "" {
		return record.TraceID + ":" + record.Primitive
	}
	if record.Timestamp != "" {
		return record.Primitive + ":" + record.Timestamp
	}
	return record.Primitive
}

func (s *Server) publishTraceStep(ctx context.Context, record runtrace.StepRecord) {
	if store, ok := s.eventStore.(runtrace.Store); ok {
		_ = store.RecordTraceStep(ctx, record)
	}
	if s.eventBus != nil {
		projected := projectTraceEvent(record)
		s.eventBus.Publish(ctx, eventing.Event{
			Type:      "trace.step",
			Source:    "trace",
			SandboxID: record.SandboxID,
			Method:    record.Primitive,
			Message:   record.StepID,
			Data:      eventing.MustJSON(projected),
		})
	}
}
