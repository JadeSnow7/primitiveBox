package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
)

func TestRPCServerCheckpointWriteRestoreVerify(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "main.txt")
	if err := os.WriteFile(file, []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(workspace, primitive.DefaultOptions())
	server := rpc.NewServer(registry, nil, nil)
	handler := server.Handler()
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthResp := httptest.NewRecorder()
	handler.ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected healthy server, got %d", healthResp.Code)
	}

	checkpointResp := callRPC(t, handler, rpc.Request{
		JSONRPC: "2.0",
		Method:  "state.checkpoint",
		Params:  mustJSON(t, map[string]any{"label": "before-change"}),
		ID:      "cp-1",
	})
	checkpointData := decodeData(t, checkpointResp.Result)
	checkpointID := checkpointData["checkpoint_id"].(string)

	callRPC(t, handler, rpc.Request{
		JSONRPC: "2.0",
		Method:  "fs.write",
		Params:  mustJSON(t, map[string]any{"path": "main.txt", "content": "v2\n"}),
		ID:      "write-1",
	})

	errorResp := callRPC(t, handler, rpc.Request{
		JSONRPC: "2.0",
		Method:  "fs.read",
		Params:  mustJSON(t, map[string]any{"path": "main.txt", "start_line": 2, "end_line": 1}),
		ID:      "read-err",
	})
	if errorResp.Error == nil {
		t.Fatalf("expected invalid params error, got %+v", errorResp)
	}
	if errorResp.Error.Code != rpc.CodeInvalidParams {
		t.Fatalf("expected invalid params code, got %d", errorResp.Error.Code)
	}

	restoreResp := callRPC(t, handler, rpc.Request{
		JSONRPC: "2.0",
		Method:  "state.restore",
		Params:  mustJSON(t, map[string]any{"checkpoint_id": checkpointID}),
		ID:      "restore-1",
	})
	restoreData := decodeData(t, restoreResp.Result)
	if restoreData["files_changed"].(float64) < 1 {
		t.Fatalf("expected restore to report changed files, got %+v", restoreData)
	}

	verifyResp := callRPC(t, handler, rpc.Request{
		JSONRPC: "2.0",
		Method:  "verify.test",
		Params:  mustJSON(t, map[string]any{"command": `test "$(cat main.txt)" = "v1"`}),
		ID:      "verify-1",
	})
	verifyData := decodeData(t, verifyResp.Result)
	if passed, ok := verifyData["passed"].(bool); !ok || !passed {
		t.Fatalf("expected verify.test to pass after restore, got %+v", verifyData)
	}
}

func callRPC(t *testing.T, handler http.Handler, req rpc.Request) rpc.Response {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal rpc request: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	httpResp := httptest.NewRecorder()
	handler.ServeHTTP(httpResp, httpReq)

	var resp rpc.Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	return resp
}

func decodeData(t *testing.T, result any) map[string]any {
	t.Helper()

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result envelope: %v", err)
	}

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode result envelope: %v", err)
	}
	return envelope.Data
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return data
}
