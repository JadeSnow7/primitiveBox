package runtrace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
)

const HeaderTraceStep = "X-PrimitiveBox-Trace-Step"

type StepRecord struct {
	TaskID           string   `json:"task_id,omitempty"`
	TraceID          string   `json:"trace_id,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	AttemptID        string   `json:"attempt_id,omitempty"`
	SandboxID        string   `json:"sandbox_id,omitempty"`
	StepID           string   `json:"step_id,omitempty"`
	Primitive        string   `json:"primitive"`
	CheckpointID     string   `json:"checkpoint_id,omitempty"`
	IntentSnapshot   string   `json:"intent_snapshot,omitempty"`
	LayerAOutcome    string   `json:"layer_a_outcome,omitempty"`
	StrategyName     string   `json:"strategy_name,omitempty"`
	StrategyOutcome  string   `json:"strategy_outcome,omitempty"`
	RecoveryPath     string   `json:"recovery_path,omitempty"`
	AffectedScopes   []string `json:"affected_scopes,omitempty"`
	CVRDepthExceeded bool     `json:"cvr_depth_exceeded,omitempty"`
	VerifyResult     string   `json:"verify_result,omitempty"`
	DurationMs       int64    `json:"duration_ms,omitempty"`
	FailureKind      string   `json:"failure_kind,omitempty"`
	Timestamp        string   `json:"timestamp,omitempty"`
}

type Recorder struct {
	mu     sync.RWMutex
	record *StepRecord
}

func (r *Recorder) Set(record StepRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyRecord := record
	r.record = &copyRecord
}

func (r *Recorder) Record() (StepRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.record == nil {
		return StepRecord{}, false
	}
	return *r.record, true
}

type recorderContextKey struct{}

func WithRecorder(ctx context.Context) (context.Context, *Recorder) {
	rec := &Recorder{}
	return context.WithValue(ctx, recorderContextKey{}, rec), rec
}

func RecorderFromContext(ctx context.Context) (*Recorder, bool) {
	if ctx == nil {
		return nil, false
	}
	rec, ok := ctx.Value(recorderContextKey{}).(*Recorder)
	return rec, ok
}

func EncodeHeader(record StepRecord) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func DecodeHeader(value string) (StepRecord, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return StepRecord{}, err
	}
	var record StepRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return StepRecord{}, err
	}
	return record, nil
}

type Store interface {
	RecordTraceStep(ctx context.Context, record StepRecord) error
}
