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

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":"{\"type\":\"object\",\"properties\":{\"name\":{\"type\":\"string\"}}}","output_schema":"{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}}}"},"id":"req-sandbox"}`)
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
	if string(manifest.InputSchema) != `{"properties":{"name":{"type":"string"}},"type":"object"}` {
		t.Fatalf("expected canonical input schema, got %s", manifest.InputSchema)
	}
	if string(manifest.OutputSchema) != `{"properties":{"message":{"type":"string"}},"type":"object"}` {
		t.Fatalf("expected canonical output schema, got %s", manifest.OutputSchema)
	}
	if manifest.Verify != nil {
		t.Fatalf("expected no verify declaration, got %+v", manifest.Verify)
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

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"}},"id":"req-proxy"}`)
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

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"myapp","name":"myapp.greet","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"}},"id":"req-missing-header"}`)
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

func TestAppRegister_ReservedNamespaceRejected(t *testing.T) {
	t.Parallel()

	resp := registerAppManifest(t, `{"app_id":"myapp","name":"fs.read","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"}}`)
	if resp.Error == nil {
		t.Fatalf("expected JSON-RPC error, got %+v", resp)
	}
	if resp.Error.Code != CodeInternalError {
		t.Fatalf("expected internal error code, got %d", resp.Error.Code)
	}
	if resp.Error.Message == "" || !bytes.Contains([]byte(resp.Error.Message), []byte("reserved system namespace")) {
		t.Fatalf("expected reserved namespace error, got %+v", resp.Error)
	}
}

func TestAppRegister_RawJSONSchemaAccepted(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	appRegistry := primitive.NewInMemoryAppRegistry()
	server.RegisterAppRegistry(appRegistry)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"kvapp","name":"kv.get","socket_path":"/tmp/kv.sock","input_schema":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]},"output_schema":{"type":"object","properties":{"value":{"type":"string"}}}},"id":"req-raw"}`)
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

	manifest, err := appRegistry.Get(context.Background(), "kv.get")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest")
	}
	if string(manifest.InputSchema) != `{"properties":{"key":{"type":"string"}},"required":["key"],"type":"object"}` {
		t.Fatalf("expected canonical raw input schema, got %s", manifest.InputSchema)
	}
}

func TestAppRegister_InvalidSchemaRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "invalid_json_string",
			payload: `{"app_id":"myapp","name":"kv.get","socket_path":"/tmp/myapp.sock","input_schema":"{","output_schema":{"type":"object"}}`,
			want:    "invalid input_schema",
		},
		{
			name:    "non_object_root",
			payload: `{"app_id":"myapp","name":"kv.get","socket_path":"/tmp/myapp.sock","input_schema":["bad"],"output_schema":{"type":"object"}}`,
			want:    "must be a JSON object",
		},
		{
			name:    "non_object_type",
			payload: `{"app_id":"myapp","name":"kv.get","socket_path":"/tmp/myapp.sock","input_schema":{"type":"array"},"output_schema":{"type":"object"}}`,
			want:    `top-level "type" must be "object"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := registerAppManifest(t, tc.payload)
			if resp.Error == nil {
				t.Fatalf("expected JSON-RPC error, got %+v", resp)
			}
			if resp.Error.Code != CodeInvalidRequest && resp.Error.Code != CodeInternalError {
				t.Fatalf("unexpected error code %d", resp.Error.Code)
			}
			if !bytes.Contains([]byte(resp.Error.Message), []byte(tc.want)) {
				t.Fatalf("expected %q in error, got %+v", tc.want, resp.Error)
			}
		})
	}
}

func TestAppRegister_LegacyVerifyEndpointMapsToVerifyPrimitive(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	appRegistry := primitive.NewInMemoryAppRegistry()
	server.RegisterAppRegistry(appRegistry)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"kvapp","name":"kv.set","socket_path":"/tmp/kv.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"verify_endpoint":"kv.get"},"id":"req-legacy-verify"}`)
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

	manifest, err := appRegistry.Get(context.Background(), "kv.set")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil || manifest.Verify == nil {
		t.Fatalf("expected verify declaration, got %+v", manifest)
	}
	if manifest.Verify.Strategy != "primitive" || manifest.Verify.Primitive != "kv.get" {
		t.Fatalf("unexpected verify mapping: %+v", manifest.Verify)
	}
}

func TestAppRegister_LegacyRollbackEndpointMapsToRollbackPrimitive(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	appRegistry := primitive.NewInMemoryAppRegistry()
	server.RegisterAppRegistry(appRegistry)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":{"app_id":"kvapp","name":"kv.set","socket_path":"/tmp/kv.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"rollback_endpoint":"kv.rollback_set"},"id":"req-legacy-rollback"}`)
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

	manifest, err := appRegistry.Get(context.Background(), "kv.set")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest == nil || manifest.Rollback == nil {
		t.Fatalf("expected rollback declaration, got %+v", manifest)
	}
	if manifest.Rollback.Strategy != "primitive" || manifest.Rollback.Primitive != "kv.rollback_set" {
		t.Fatalf("unexpected rollback mapping: %+v", manifest.Rollback)
	}
}

func TestAppRegister_InvalidVerifyRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "command_missing_command",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"verify":{"strategy":"command"}}`,
			want:    `verify.strategy "command" requires verify.command`,
		},
		{
			name:    "none_with_primitive",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"verify":{"strategy":"none","primitive":"kv.get"}}`,
			want:    `verify.strategy "none" cannot include verify.primitive or verify.command`,
		},
		{
			name:    "legacy_conflicts_with_command",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"verify_endpoint":"kv.get","verify":{"strategy":"command","command":"true"}}`,
			want:    `verify_endpoint cannot be combined with verify.strategy="command"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := registerAppManifest(t, tc.payload)
			if resp.Error == nil {
				t.Fatalf("expected JSON-RPC error, got %+v", resp)
			}
			if !bytes.Contains([]byte(resp.Error.Message), []byte(tc.want)) {
				t.Fatalf("expected %q in error, got %+v", tc.want, resp.Error)
			}
		})
	}
}

func TestAppRegister_InvalidRollbackRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "primitive_missing_primitive",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"rollback":{"strategy":"primitive"}}`,
			want:    `rollback.strategy "primitive" requires rollback.primitive`,
		},
		{
			name:    "none_with_primitive",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"rollback":{"strategy":"none","primitive":"kv.rollback_set"}}`,
			want:    `rollback.strategy "none" cannot include rollback.primitive`,
		},
		{
			name:    "legacy_conflicts_with_none",
			payload: `{"app_id":"myapp","name":"kv.set","socket_path":"/tmp/myapp.sock","input_schema":{"type":"object"},"output_schema":{"type":"object"},"rollback_endpoint":"kv.rollback_set","rollback":{"strategy":"none"}}`,
			want:    `rollback_endpoint cannot be combined with rollback.strategy="none"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := registerAppManifest(t, tc.payload)
			if resp.Error == nil {
				t.Fatalf("expected JSON-RPC error, got %+v", resp)
			}
			if !bytes.Contains([]byte(resp.Error.Message), []byte(tc.want)) {
				t.Fatalf("expected %q in error, got %+v", tc.want, resp.Error)
			}
		})
	}
}

func registerAppManifest(t *testing.T, params string) Response {
	t.Helper()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	appRegistry := primitive.NewInMemoryAppRegistry()
	server.RegisterAppRegistry(appRegistry)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"app.register","params":` + params + `,"id":"req-register"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc", body)
	req.Header.Set("X-PB-Origin", "sandbox")
	w := httptest.NewRecorder()

	server.handleRPC(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
