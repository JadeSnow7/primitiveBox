package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtimectx"
	"primitivebox/internal/runtrace"

	"github.com/google/uuid"
)

// IntentContextKey is the public context key used to pass *cvr.PrimitiveIntent
// through runtime execution paths.
var IntentContextKey = runtimectx.IntentContextKey

type Config struct {
	WorkspaceDir string
	AppsDir      string
	LogDir       string
	DataDir      string
	SandboxID    string
	Options      primitive.Options
}

type Runtime struct {
	config       Config
	raw          map[string]primitive.Primitive
	registry     *primitive.Registry
	executor     *SerialExecutor
	traceWriter  *TraceWriter
	checkpointer *primitive.StateCheckpoint
	restorer     *primitive.StateRestore
	adapters     []*AdapterProcess
	schemas      map[string]primitive.Schema
	mu           sync.RWMutex
}

func New(config Config) (*Runtime, error) {
	rt := &Runtime{
		config:       config,
		raw:          make(map[string]primitive.Primitive),
		registry:     primitive.NewRegistry(),
		executor:     NewSerialExecutor(),
		traceWriter:  NewTraceWriter(config.LogDir),
		checkpointer: primitive.NewStateCheckpoint(config.WorkspaceDir),
		restorer:     primitive.NewStateRestore(config.WorkspaceDir),
		schemas:      make(map[string]primitive.Schema),
	}

	if err := rt.registerSystemPrimitives(); err != nil {
		return nil, err
	}
	if err := rt.loadAdapters(); err != nil {
		return nil, err
	}
	return rt, nil
}

func (r *Runtime) Registry() *primitive.Registry {
	return r.registry
}

func (r *Runtime) Close() error {
	var firstErr error
	for _, adapter := range r.adapters {
		if err := adapter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.executor.Close()
	return firstErr
}

func (r *Runtime) registerSystemPrimitives() error {
	rawRegistry := primitive.NewRegistry()
	rawRegistry.RegisterDefaults(r.config.WorkspaceDir, r.config.Options)
	rawRegistry.RegisterSandboxExtras(r.config.WorkspaceDir, r.config.Options)

	for _, p := range rawRegistry.Registered() {
		if err := r.registerRawPrimitive(p, primitive.EnrichSchema(p.Schema())); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) registerRawPrimitive(p primitive.Primitive, schema primitive.Schema) error {
	r.raw[p.Name()] = p
	r.schemas[p.Name()] = schema
	return r.registry.Register(&managedPrimitive{
		name:     p.Name(),
		category: p.Category(),
		runtime:  r,
	})
}

func (r *Runtime) Execute(ctx context.Context, method string, params json.RawMessage) (primitive.Result, error) {
	return r.executor.Do(ctx, func() (primitive.Result, error) {
		return r.execute(ctx, method, params, false)
	})
}

func (r *Runtime) execute(ctx context.Context, method string, params json.RawMessage, internal bool) (primitive.Result, error) {
	r.mu.RLock()
	p, ok := r.raw[method]
	schema, schemaOK := r.schemas[method]
	r.mu.RUnlock()
	if !ok || !schemaOK {
		return primitive.Result{}, &primitive.PrimitiveError{Code: primitive.ErrNotFound, Message: "primitive not found: " + method}
	}

	start := time.Now()
	schema = primitive.EnrichSchema(schema)
	record := runtrace.StepRecord{
		TaskID:       "task-" + uuid.NewString()[:8],
		TraceID:      "trace-" + uuid.NewString()[:8],
		SessionID:    "session-" + uuid.NewString()[:8],
		AttemptID:    "attempt-1",
		SandboxID:    r.config.SandboxID,
		StepID:       "step-" + uuid.NewString()[:8],
		Primitive:    method,
		VerifyResult: "not_run",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	callCtx := ctx
	if schema.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(schema.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	if intent, ok := ctx.Value(IntentContextKey).(*cvr.PrimitiveIntent); ok && intent != nil {
		callCtx = runtimectx.WithIntent(callCtx, intent)
	}

	checkpointID := ""
	if !internal && schema.CheckpointRequired && method != "state.checkpoint" && method != "state.restore" {
		cpLabel := fmt.Sprintf("%s-%d", strings.ReplaceAll(method, ".", "-"), time.Now().UnixMilli())
		cpParams, _ := json.Marshal(map[string]any{"label": cpLabel})
		cpResult, cpErr := r.checkpointer.Execute(callCtx, cpParams)
		if cpErr != nil {
			record.FailureKind = "environment"
			record.DurationMs = time.Since(start).Milliseconds()
			r.persistTrace(ctx, record)
			return primitive.Result{}, cpErr
		}
		if cpData, ok := cpResult.Data.(primitive.CheckpointResult); ok {
			checkpointID = cpData.CheckpointID
		}
		record.CheckpointID = checkpointID
	}

	result, err := p.Execute(callCtx, params)
	failureKind := classifyFailure(method, result, err, callCtx)
	verifyResult := "not_run"

	if !internal && err == nil && schema.VerifierHint != "" {
		if _, ok := r.raw[schema.VerifierHint]; ok {
			verifyResult = "passed"
			verifyResultRaw, verifyErr := r.execute(ctx, schema.VerifierHint, nil, true)
			if verifyErr != nil || !extractPassed(verifyResultRaw) {
				verifyResult = "failed"
				if verifyErr != nil {
					err = verifyErr
				} else {
					err = &primitive.PrimitiveError{Code: primitive.ErrExecution, Message: "verifier failed: " + schema.VerifierHint}
				}
				failureKind = "test_fail"
			}
		}
	}

	if !internal && checkpointID != "" && shouldRestoreAfterFailure(schema, failureKind, err) {
		restoreParams, _ := json.Marshal(map[string]any{"checkpoint_id": checkpointID})
		_, _ = r.restorer.Execute(ctx, restoreParams)
	}

	record.VerifyResult = verifyResult
	if intent, ok := ctx.Value(IntentContextKey).(*cvr.PrimitiveIntent); ok && intent != nil {
		record.AffectedScopes = append([]string(nil), intent.AffectedScopes...)
	}
	record.DurationMs = time.Since(start).Milliseconds()
	record.FailureKind = failureKind
	r.persistTrace(ctx, record)

	return result, err
}

func (r *Runtime) persistTrace(ctx context.Context, record runtrace.StepRecord) {
	if rec, ok := runtrace.RecorderFromContext(ctx); ok {
		rec.Set(record)
	}
	r.traceWriter.Append(record)
}

type managedPrimitive struct {
	name     string
	category string
	runtime  *Runtime
}

func (m *managedPrimitive) Name() string     { return m.name }
func (m *managedPrimitive) Category() string { return m.category }
func (m *managedPrimitive) Schema() primitive.Schema {
	m.runtime.mu.RLock()
	defer m.runtime.mu.RUnlock()
	return primitive.EnrichSchema(m.runtime.schemas[m.name])
}
func (m *managedPrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	return m.runtime.Execute(ctx, m.name, params)
}

func classifyFailure(method string, result primitive.Result, err error, ctx context.Context) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "timeout"
	}
	if err == nil {
		if (method == "verify.test" || method == "test.run" || method == "repo.run_tests") && !extractPassed(result) {
			return "test_fail"
		}
		return ""
	}
	if pe, ok := err.(*primitive.PrimitiveError); ok {
		switch pe.Code {
		case primitive.ErrPermission:
			return "policy_denied"
		case primitive.ErrTimeout:
			return "timeout"
		case primitive.ErrValidation:
			return "syntax"
		default:
			if strings.Contains(strings.ToLower(pe.Message), "timeout") {
				return "timeout"
			}
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "timeout"
	}
	if strings.Contains(strings.ToLower(err.Error()), "adapter") {
		return "environment"
	}
	return "unknown"
}

func shouldRestoreAfterFailure(schema primitive.Schema, failureKind string, err error) bool {
	if err == nil {
		return false
	}
	if schema.SideEffect != primitive.SideEffectWrite && schema.SideEffect != primitive.SideEffectExec {
		return false
	}
	switch failureKind {
	case "timeout", "test_fail", "unknown", "environment", "syntax":
		return true
	default:
		return false
	}
}

func extractPassed(result primitive.Result) bool {
	if result.Data == nil {
		return false
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return false
	}
	passed, ok := data["passed"].(bool)
	if ok {
		return passed
	}
	return false
}

type SerialExecutor struct {
	jobs chan job
}

type job struct {
	ctx    context.Context
	fn     func() (primitive.Result, error)
	result chan jobResult
}

type jobResult struct {
	result primitive.Result
	err    error
}

func NewSerialExecutor() *SerialExecutor {
	exec := &SerialExecutor{jobs: make(chan job)}
	go func() {
		for j := range exec.jobs {
			res, err := j.fn()
			j.result <- jobResult{result: res, err: err}
		}
	}()
	return exec
}

func (s *SerialExecutor) Do(ctx context.Context, fn func() (primitive.Result, error)) (primitive.Result, error) {
	resp := make(chan jobResult, 1)
	select {
	case s.jobs <- job{ctx: ctx, fn: fn, result: resp}:
	case <-ctx.Done():
		return primitive.Result{}, ctx.Err()
	}
	select {
	case out := <-resp:
		return out.result, out.err
	case <-ctx.Done():
		return primitive.Result{}, ctx.Err()
	}
}

func (s *SerialExecutor) Close() {
	close(s.jobs)
}

type TraceWriter struct {
	path string
	mu   sync.Mutex
}

func NewTraceWriter(logDir string) *TraceWriter {
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "primitivebox")
	}
	return &TraceWriter{
		path: filepath.Join(logDir, "trace.jsonl"),
	}
}

func (w *TraceWriter) Append(record runtrace.StepRecord) {
	if record.Primitive == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(w.path), 0o755)
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}
