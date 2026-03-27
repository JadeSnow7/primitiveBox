package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtrace"
	"primitivebox/internal/sandbox"
)

func TestSandboxRPCProxy(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-proxy1": sandbox.StatusRunning,
		},
	}, t.TempDir())
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-proxy1",
		ContainerID:  "ctr-proxy1",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/rpc" {
			t.Fatalf("unexpected proxied path: %s", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), `"method":"fs.read"`) {
			t.Fatalf("unexpected proxied body: %s", string(body))
		}
		respBody, _ := json.Marshal(Response{
			JSONRPC: "2.0",
			Result: map[string]any{
				"data": map[string]any{"content": "sandbox"},
			},
			ID: "req-1",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
	})

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"fs.read","params":{"path":"main.txt"},"id":"req-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/sb-proxy1/rpc", body)
	w := httptest.NewRecorder()

	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected proxy success, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSandboxRPCProxyPersistsTraceHeader(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-proxy-trace": sandbox.StatusRunning,
		},
	}, t.TempDir())
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-proxy-trace",
		ContainerID:  "ctr-proxy-trace",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	traceStore := &fakeTraceStore{}
	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.AttachEventing(eventing.NewBus(nil), traceStore)
	server.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		record := runtrace.StepRecord{
			SandboxID:  "sb-proxy-trace",
			Primitive:  "repo.patch_symbol",
			TraceID:    "trace-123",
			SessionID:  "session-123",
			AttemptID:  "attempt-1",
			StepID:     "step-123",
			Timestamp:  "2026-03-16T00:00:00Z",
			DurationMs: 12,
		}
		encoded, _ := runtrace.EncodeHeader(record)
		respBody, _ := json.Marshal(Response{
			JSONRPC: "2.0",
			Result:  map[string]any{"data": map[string]any{"ok": true}},
			ID:      "req-trace",
		})
		header := make(http.Header)
		header.Set(runtrace.HeaderTraceStep, encoded)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
	})

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"repo.patch_symbol","params":{"path":"main.go"},"id":"req-trace"}`)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/sb-proxy-trace/rpc", body)
	w := httptest.NewRecorder()

	server.Handler().ServeHTTP(w, req)
	if len(traceStore.records) != 1 {
		t.Fatalf("expected one trace record, got %d", len(traceStore.records))
	}
	if traceStore.records[0].Primitive != "repo.patch_symbol" {
		t.Fatalf("unexpected trace record: %+v", traceStore.records[0])
	}
}

func TestSandboxHealthProxyRequiresRunningSandbox(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-stopped": sandbox.StatusStopped,
		},
	}, t.TempDir())
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-stopped",
		Status:       sandbox.StatusStopped,
		HealthStatus: "stopped",
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	server := NewServer(primitive.NewRegistry(), nil, manager)
	req := httptest.NewRequest(http.MethodGet, "/sandboxes/sb-stopped/health", nil)
	w := httptest.NewRecorder()

	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected conflict for stopped sandbox, got %d", w.Code)
	}
}

func TestSandboxRPCProxyRecoversFromManagerPanic(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(panicProxyDriver{}, t.TempDir())
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-panic",
		ContainerID:  "ctr-panic",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
	server := NewServer(primitive.NewRegistry(), nil, manager)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"fs.read","params":{"path":"main.txt"},"id":"req-panic"}`)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/sb-panic/rpc", body)
	w := httptest.NewRecorder()

	server.Handler().ServeHTTP(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInternalError {
		t.Fatalf("expected structured panic recovery, got %+v", resp)
	}
}

type proxyDriver struct {
	statuses map[string]sandbox.SandboxStatus
}

func (proxyDriver) Create(ctx context.Context, config sandbox.SandboxConfig) (*sandbox.Sandbox, error) {
	return nil, nil
}
func (proxyDriver) Start(ctx context.Context, sandboxID string) error   { return nil }
func (proxyDriver) Stop(ctx context.Context, sandboxID string) error    { return nil }
func (proxyDriver) Destroy(ctx context.Context, sandboxID string) error { return nil }
func (proxyDriver) Exec(ctx context.Context, sandboxID string, cmd sandbox.ExecCommand) (*sandbox.ExecResult, error) {
	return &sandbox.ExecResult{}, nil
}
func (p proxyDriver) Inspect(ctx context.Context, sandboxID string) (*sandbox.Sandbox, error) {
	status := sandbox.StatusRunning
	if current, ok := p.statuses[sandboxID]; ok {
		status = current
	}
	return &sandbox.Sandbox{
		ID:          sandboxID,
		Status:      status,
		RPCEndpoint: "http://sandbox.local",
	}, nil
}
func (p proxyDriver) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	if status, ok := p.statuses[sandboxID]; ok {
		return status, nil
	}
	return sandbox.StatusRunning, nil
}
func (proxyDriver) Capabilities() []sandbox.RuntimeCapability { return nil }
func (proxyDriver) Name() string                              { return "proxy" }

type panicProxyDriver struct{}

func (panicProxyDriver) Create(ctx context.Context, config sandbox.SandboxConfig) (*sandbox.Sandbox, error) {
	return nil, errors.New("not implemented")
}
func (panicProxyDriver) Start(ctx context.Context, sandboxID string) error   { return nil }
func (panicProxyDriver) Stop(ctx context.Context, sandboxID string) error    { return nil }
func (panicProxyDriver) Destroy(ctx context.Context, sandboxID string) error { return nil }
func (panicProxyDriver) Exec(ctx context.Context, sandboxID string, cmd sandbox.ExecCommand) (*sandbox.ExecResult, error) {
	return nil, errors.New("not implemented")
}
func (panicProxyDriver) Inspect(ctx context.Context, sandboxID string) (*sandbox.Sandbox, error) {
	panic("inspect boom")
}
func (panicProxyDriver) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	panic("status boom")
}
func (panicProxyDriver) Capabilities() []sandbox.RuntimeCapability { return nil }
func (panicProxyDriver) Name() string                              { return "panic-proxy" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type fakeTraceStore struct {
	records []runtrace.StepRecord
}

func (f *fakeTraceStore) Append(ctx context.Context, evt eventing.Event) (eventing.Event, error) {
	return evt, nil
}

func (f *fakeTraceStore) ListEvents(ctx context.Context, filter eventing.ListFilter) ([]eventing.Event, error) {
	return nil, nil
}

func (f *fakeTraceStore) RecordTraceStep(ctx context.Context, record runtrace.StepRecord) error {
	f.records = append(f.records, record)
	return nil
}

func (f *fakeTraceStore) ListTraceSteps(ctx context.Context, sandboxID string, limit int) ([]runtrace.StepRecord, error) {
	var out []runtrace.StepRecord
	for _, record := range f.records {
		if sandboxID != "" && record.SandboxID != sandboxID {
			continue
		}
		out = append(out, record)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeTraceStore) GetTraceStep(ctx context.Context, sandboxID, stepID string) (*runtrace.StepRecord, error) {
	for _, record := range f.records {
		if sandboxID != "" && record.SandboxID != sandboxID {
			continue
		}
		if record.StepID == stepID {
			copyRecord := record
			return &copyRecord, nil
		}
	}
	return nil, nil
}
