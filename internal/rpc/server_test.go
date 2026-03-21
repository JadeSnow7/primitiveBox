package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

func TestHandleRPCRecoversFromPrimitivePanic(t *testing.T) {
	t.Parallel()

	registry := primitive.NewRegistry()
	registry.MustRegister(panicPrimitive{})
	server := NewServer(registry, nil, nil)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"panic.exec","params":{},"id":"req-1"}`)
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
	if resp.Error.Code != CodeInternalError {
		t.Fatalf("expected internal error code, got %d", resp.Error.Code)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthResp := httptest.NewRecorder()
	server.handleHealth(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected health endpoint to remain available, got %d", healthResp.Code)
	}
}

func TestListPrimitivesIncludesAppAvailability(t *testing.T) {
	t.Parallel()

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(t.TempDir(), primitive.DefaultOptions())

	appRegistry := primitive.NewInMemoryAppRegistry()
	if err := appRegistry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.get",
		Description:  "Fetch a key.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	}); err != nil {
		t.Fatalf("register app primitive: %v", err)
	}
	if err := appRegistry.MarkUnavailable(context.Background(), "kv-app"); err != nil {
		t.Fatalf("mark app unavailable: %v", err)
	}

	server := NewServer(registry, nil, nil)
	server.RegisterAppRegistry(appRegistry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/primitives", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	var resp struct {
		Primitives []map[string]any `json:"primitives"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode primitives response: %v", err)
	}
	if len(resp.Primitives) == 0 {
		t.Fatal("expected primitives in response")
	}
	found := false
	for _, item := range resp.Primitives {
		if item["name"] != "kv.get" {
			continue
		}
		found = true
		if item["status"] != string(primitive.AppPrimitiveUnavailable) {
			t.Fatalf("expected unavailable status, got %#v", item)
		}
	}
	if !found {
		t.Fatalf("expected kv.get in primitive list, got %#v", resp.Primitives)
	}
}

type panicPrimitive struct{}

func (panicPrimitive) Name() string { return "panic.exec" }

func (panicPrimitive) Category() string { return "test" }

func (panicPrimitive) Schema() primitive.Schema { return primitive.Schema{} }

func (panicPrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	panic("boom")
}
