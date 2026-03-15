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

	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
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
