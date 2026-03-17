package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtrace"
	"primitivebox/internal/sandbox"
)

func TestRPCStreamShellExecEmitsSSEFrames(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := primitive.NewRegistry()
	registry.RegisterDefaults(workspace, primitive.DefaultOptions())

	store := &memoryEventStore{}
	server := NewServer(registry, nil, nil)
	server.AttachEventing(eventing.NewBus(store), store)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"shell.exec","params":{"command":"printf 'hello\\n'"},"id":"stream-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc/stream", body)
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	payload := resp.Body.String()
	if !strings.Contains(payload, "event: started") {
		t.Fatalf("expected started event, got %s", payload)
	}
	if !strings.Contains(payload, "event: stdout") {
		t.Fatalf("expected stdout event, got %s", payload)
	}
	if !strings.Contains(payload, "event: completed") {
		t.Fatalf("expected completed event, got %s", payload)
	}
}

func TestAPIEventsListsPersistedEvents(t *testing.T) {
	t.Parallel()

	store := &memoryEventStore{
		events: []eventing.Event{
			{ID: 1, Type: "rpc.started", SandboxID: "sb-1"},
			{ID: 2, Type: "shell.output", SandboxID: "sb-1"},
		},
	}
	server := NewServer(primitive.NewRegistry(), nil, nil)
	server.AttachEventing(eventing.NewBus(store), store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?sandbox_id=sb-1&limit=10", nil)
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"shell.output"`) {
		t.Fatalf("expected shell.output event, got %s", resp.Body.String())
	}
}

func TestAPISandboxTreeUsesSandboxRPC(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithOptions(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-tree": sandbox.StatusRunning,
		},
	}, sandbox.ManagerOptions{Store: sandbox.NewMemoryStore()})
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-tree",
		Driver:       "proxy",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
		Config:       sandbox.SandboxConfig{Driver: "proxy"},
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		respBody, _ := json.Marshal(Response{
			JSONRPC: "2.0",
			Result: map[string]any{
				"data": map[string]any{
					"entries": []map[string]any{{"name": "README.md", "path": "README.md", "is_dir": false, "size": 10}},
				},
			},
			ID: "inspector",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-tree/tree", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "README.md") {
		t.Fatalf("expected README entry, got %s", resp.Body.String())
	}
}

func TestAPITraceListAndDetail(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithOptions(proxyDriver{}, sandbox.ManagerOptions{Store: sandbox.NewMemoryStore()})
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-trace",
		Driver:       "proxy",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
		Config:       sandbox.SandboxConfig{Driver: "proxy"},
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	traceStore := &fakeTraceStore{
		records: []runtrace.StepRecord{
			{
				SandboxID:       "sb-trace",
				StepID:          "step-1",
				TraceID:         "trace-1",
				Primitive:       "repo.patch_symbol",
				IntentSnapshot:  `{"category":"mutation","reversible":true,"risk_level":"medium","affected_scopes":["main.go"]}`,
				LayerAOutcome:   "checkpoint_created",
				StrategyName:    "verify.test",
				StrategyOutcome: "passed",
				RecoveryPath:    "",
				AffectedScopes:  []string{"main.go"},
				DurationMs:      24,
				Timestamp:       "2026-03-16T00:00:00Z",
			},
		},
	}
	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.AttachEventing(eventing.NewBus(traceStore), traceStore)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-trace/trace", nil)
	listResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listResp.Code, listResp.Body.String())
	}
	if !strings.Contains(listResp.Body.String(), `"primitive_id":"repo.patch_symbol"`) {
		t.Fatalf("expected projected trace event, got %s", listResp.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-trace/trace/step-1", nil)
	detailResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResp, detailReq)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", detailResp.Code, detailResp.Body.String())
	}
	if !strings.Contains(detailResp.Body.String(), `"category":"mutation"`) {
		t.Fatalf("expected structured intent snapshot, got %s", detailResp.Body.String())
	}
}

func TestAPITraceStreamEmitsProjectedTraceEvents(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithOptions(proxyDriver{}, sandbox.ManagerOptions{Store: sandbox.NewMemoryStore()})
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-trace",
		Driver:       "proxy",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
		Config:       sandbox.SandboxConfig{Driver: "proxy"},
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	traceStore := &fakeTraceStore{}
	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.AttachEventing(eventing.NewBus(traceStore), traceStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-trace/trace/stream", nil).WithContext(ctx)
	resp := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(resp, req)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)

	server.publishTraceStep(context.Background(), runtrace.StepRecord{
		SandboxID:       "sb-trace",
		StepID:          "step-stream",
		TraceID:         "trace-stream",
		Primitive:       "fs.write",
		StrategyOutcome: "passed",
		Timestamp:       "2026-03-16T00:00:00Z",
	})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	payload := resp.Body.String()
	if !strings.Contains(payload, "event: trace.step") {
		t.Fatalf("expected trace.step event, got %s", payload)
	}
	if !strings.Contains(payload, `"step-stream"`) {
		t.Fatalf("expected projected step id, got %s", payload)
	}
}

func TestAPIAppPrimitivesProxyListsSandboxManifests(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithOptions(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-app": sandbox.StatusRunning,
		},
	}, sandbox.ManagerOptions{Store: sandbox.NewMemoryStore()})
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-app",
		Driver:       "proxy",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
		Config:       sandbox.SandboxConfig{Driver: "proxy"},
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/app-primitives" {
			t.Fatalf("unexpected proxied path: %s", req.URL.Path)
		}
		respBody, _ := json.Marshal(appPrimitiveListResponse{
			AppPrimitives: []primitive.AppPrimitiveManifest{
				{
					AppID:       "notes",
					Name:        "notes.create",
					Description: "Create a note",
					InputSchema: json.RawMessage(`{"type":"object"}`),
					OutputSchema: json.RawMessage(`{"type":"object"}`),
					SocketPath:  "/tmp/notes.sock",
				},
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-app/app-primitives", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"notes.create"`) {
		t.Fatalf("expected app primitive manifest, got %s", resp.Body.String())
	}
}

type memoryEventStore struct {
	events []eventing.Event
}

func (m *memoryEventStore) Append(ctx context.Context, evt eventing.Event) (eventing.Event, error) {
	evt.ID = int64(len(m.events) + 1)
	m.events = append(m.events, evt)
	return evt, nil
}

func (m *memoryEventStore) ListEvents(ctx context.Context, filter eventing.ListFilter) ([]eventing.Event, error) {
	var out []eventing.Event
	for _, evt := range m.events {
		if filter.SandboxID != "" && evt.SandboxID != filter.SandboxID {
			continue
		}
		out = append(out, evt)
	}
	return out, nil
}
