package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

type panicPrimitive struct{}

func (panicPrimitive) Name() string { return "panic.exec" }

func (panicPrimitive) Category() string { return "test" }

func (panicPrimitive) Schema() primitive.Schema { return primitive.Schema{} }

func (panicPrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	panic("boom")
}
