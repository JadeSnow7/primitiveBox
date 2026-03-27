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
		Name:         "demo.tabular_data",
		Description:  "Fetch a key.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		UILayoutHint: "table",
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
		if item["name"] != "demo.tabular_data" {
			continue
		}
		found = true
		if item["status"] != string(primitive.AppPrimitiveUnavailable) {
			t.Fatalf("expected unavailable status, got %#v", item)
		}
		if item["ui_layout_hint"] != "table" {
			t.Fatalf("expected ui_layout_hint to be preserved, got %#v", item)
		}
		intent, ok := item["intent"].(map[string]any)
		if !ok {
			t.Fatalf("expected intent block for app primitive, got %#v", item)
		}
		if intent["risk_level"] != string(cvr.RiskLow) {
			t.Fatalf("expected low risk intent, got %#v", intent)
		}
		if intent["reversible"] != true {
			t.Fatalf("expected reversible app primitive, got %#v", intent)
		}
	}
	if !found {
		t.Fatalf("expected demo.tabular_data in primitive list, got %#v", resp.Primitives)
	}
}

func TestListPrimitivesIncludesSystemIntentMetadata(t *testing.T) {
	t.Parallel()

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(t.TempDir(), primitive.DefaultOptions())
	server := NewServer(registry, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/primitives", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	var resp struct {
		Primitives []map[string]any `json:"primitives"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode primitives response: %v", err)
	}

	for _, item := range resp.Primitives {
		if item["name"] != "fs.write" {
			continue
		}
		intent, ok := item["intent"].(map[string]any)
		if !ok {
			t.Fatalf("expected intent block for system primitive, got %#v", item)
		}
		if intent["risk_level"] != string(cvr.RiskHigh) {
			t.Fatalf("expected high risk fs.write, got %#v", intent)
		}
		if intent["reversible"] != false {
			t.Fatalf("expected irreversible fs.write intent, got %#v", intent)
		}
		if intent["side_effect"] != primitive.SideEffectWrite {
			t.Fatalf("expected write side effect, got %#v", intent)
		}
		return
	}

	t.Fatalf("expected fs.write in primitive list, got %#v", resp.Primitives)
}

func TestListPrimitivesIncludesDBAndBrowserSchemaHints(t *testing.T) {
	t.Parallel()

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(t.TempDir(), primitive.DefaultOptions())
	registry.RegisterSandboxExtras(t.TempDir(), primitive.DefaultOptions())
	server := NewServer(registry, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/primitives", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	var resp struct {
		Primitives []map[string]any `json:"primitives"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode primitives response: %v", err)
	}

	var foundDBQuery, foundDBExecute, foundBrowserGoto, foundBrowserRead bool
	for _, item := range resp.Primitives {
		switch item["name"] {
		case "db.query":
			foundDBQuery = true
			if item["ui_layout_hint"] != "table" {
				t.Fatalf("expected db.query ui_layout_hint=table, got %#v", item)
			}
			intent, _ := item["intent"].(map[string]any)
			if intent["risk_level"] != string(cvr.RiskLow) || intent["side_effect"] != primitive.SideEffectRead {
				t.Fatalf("expected db.query low/read intent, got %#v", item)
			}
		case "db.execute":
			foundDBExecute = true
			intent, _ := item["intent"].(map[string]any)
			if intent["risk_level"] != string(cvr.RiskHigh) || intent["reversible"] != false || intent["side_effect"] != primitive.SideEffectExec {
				t.Fatalf("expected db.execute high/irreversible/exec intent, got %#v", item)
			}
		case "browser.goto":
			foundBrowserGoto = true
			if item["ui_layout_hint"] != "markdown" {
				t.Fatalf("expected browser.goto ui_layout_hint=markdown, got %#v", item)
			}
		case "browser.read":
			foundBrowserRead = true
			if item["ui_layout_hint"] != "markdown" {
				t.Fatalf("expected browser.read ui_layout_hint=markdown, got %#v", item)
			}
		}
	}

	if !foundDBQuery || !foundDBExecute || !foundBrowserGoto || !foundBrowserRead {
		t.Fatalf("expected db/browser primitives in catalog, got %#v", resp.Primitives)
	}
}

type panicPrimitive struct{}

func (panicPrimitive) Name() string { return "panic.exec" }

func (panicPrimitive) Category() string { return "test" }

func (panicPrimitive) Schema() primitive.Schema { return primitive.Schema{} }

func (panicPrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	panic("boom")
}
