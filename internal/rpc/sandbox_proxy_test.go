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

	"primitivebox/internal/primitive"
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
func (p proxyDriver) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	if status, ok := p.statuses[sandboxID]; ok {
		return status, nil
	}
	return sandbox.StatusRunning, nil
}
func (proxyDriver) Name() string { return "proxy" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
