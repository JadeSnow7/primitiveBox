package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"primitivebox/internal/primitive"
	"primitivebox/internal/sandbox"
)

func TestAppRegister_RejectOnHostGateway(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(&proxyDriver{}, t.TempDir())
	server := NewServer(primitive.NewRegistry(), nil, manager)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock"},"id":"req-host"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc", body)
	w := httptest.NewRecorder()

	server.handleRPC(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected JSON-RPC error, got %+v", resp)
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method-not-found code, got %d", resp.Error.Code)
	}
}

func TestAppRegister_SandboxSuccess(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	appRegistry := primitive.NewInMemoryAppRegistry()
	server.RegisterAppRegistry(appRegistry)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":"{}","output_schema":"{}"},"id":"req-sandbox"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc", body)
	req.Header.Set("X-PB-Origin", "sandbox")
	w := httptest.NewRecorder()

	server.handleRPC(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %#v", resp.Result)
	}
	if result["registered"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}

	manifest, err := appRegistry.Get(context.Background(), "myapp.greet")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil {
		t.Fatalf("expected registered manifest")
	}
	if manifest.SocketPath != "/tmp/myapp.sock" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
}

func TestAppRegister_ProxiedSandboxSuccess(t *testing.T) {
	t.Parallel()

	manager := sandbox.NewManagerWithRegistryDir(&proxyDriver{
		statuses: map[string]sandbox.SandboxStatus{
			"sb-app-register": sandbox.StatusRunning,
		},
	}, t.TempDir())
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           "sb-app-register",
		ContainerID:  "ctr-app-register",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://sandbox.local",
		RPCPort:      18080,
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}

	server := NewServer(primitive.NewRegistry(), nil, manager)
	server.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-PB-Origin"); got != "sandbox" {
			t.Fatalf("expected X-PB-Origin to be proxied, got %q", got)
		}
		body, _ := io.ReadAll(req.Body)
		if !bytes.Contains(body, []byte(`"method":"app.register"`)) {
			t.Fatalf("unexpected proxied body: %s", string(body))
		}
		respBody, _ := json.Marshal(Response{
			JSONRPC: "2.0",
			Result: map[string]any{
				"registered": true,
				"name":       "myapp.greet",
			},
			ID: "req-proxy",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
	})

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":"{}","output_schema":"{}"},"id":"req-proxy"}`)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/sb-app-register/rpc", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")
	w := httptest.NewRecorder()

	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected proxy success, got %d: %s", w.Code, w.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
}

func TestAppRegister_MissingSandboxHeaderRejected(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":"{}","output_schema":"{}"},"id":"req-missing-header"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc", body)
	w := httptest.NewRecorder()

	server.handleRPC(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected JSON-RPC error, got %+v", resp)
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method-not-found code, got %d", resp.Error.Code)
	}
}
