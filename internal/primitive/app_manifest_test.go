package primitive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAppRegistry_DuplicateRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	first := AppPrimitiveManifest{
		AppID:        "app-one",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/app-one.sock",
	}
	second := AppPrimitiveManifest{
		AppID:        "app-two",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/app-two.sock",
	}

	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := registry.Register(context.Background(), second)
	if err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if !strings.Contains(err.Error(), first.Name) {
		t.Fatalf("expected error to contain primitive name %q, got %v", first.Name, err)
	}
	if !strings.Contains(err.Error(), first.AppID) {
		t.Fatalf("expected error to contain app id %q, got %v", first.AppID, err)
	}
}

func TestAppRegistry_SameAppIDReregistrationAllowed(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	first := AppPrimitiveManifest{
		AppID:        "app-one",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/app-one.sock",
	}
	updated := AppPrimitiveManifest{
		AppID:        "app-one",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
		SocketPath:   "/tmp/app-one-v2.sock",
	}

	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	if err := registry.Register(context.Background(), updated); err != nil {
		t.Fatalf("same-app re-register: %v", err)
	}
	got, err := registry.Get(context.Background(), "myapp.greet")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.SocketPath != updated.SocketPath {
		t.Fatalf("expected updated SocketPath %q, got %v", updated.SocketPath, got)
	}
}

func TestAppRegistry_UnregisterThenReregister(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	manifest := AppPrimitiveManifest{
		AppID:        "app-one",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/app-one.sock",
	}
	replacement := AppPrimitiveManifest{
		AppID:        "app-two",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/app-two.sock",
	}

	if err := registry.Register(context.Background(), manifest); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	if err := registry.Unregister(context.Background(), manifest.AppID, manifest.Name); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if err := registry.Register(context.Background(), replacement); err != nil {
		t.Fatalf("reregister: %v", err)
	}
}

func TestAppRegistry_ReservedNamespaceRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "fs.read",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
	})
	if err == nil {
		t.Fatal("expected reserved namespace registration to fail")
	}
	if !strings.Contains(err.Error(), "app_primitive_reserved_namespace") {
		t.Fatalf("expected reserved namespace error, got %v", err)
	}
	if !strings.Contains(err.Error(), `"fs.read"`) {
		t.Fatalf("expected primitive name in error, got %v", err)
	}
}

func TestAppRegistry_StringifiedSchemaNormalized(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.get",
		InputSchema:  json.RawMessage(`"{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\"}},\"required\":[\"key\"]}"`),
		OutputSchema: json.RawMessage(`"{\"type\":\"object\",\"properties\":{\"value\":{\"type\":\"string\"}}}"`),
		SocketPath:   "/tmp/kv.sock",
	})
	if err != nil {
		t.Fatalf("register stringified schema: %v", err)
	}

	manifest, err := registry.Get(context.Background(), "kv.get")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest")
	}
	if string(manifest.InputSchema) != `{"properties":{"key":{"type":"string"}},"required":["key"],"type":"object"}` {
		t.Fatalf("expected canonical input schema, got %s", manifest.InputSchema)
	}
	if string(manifest.OutputSchema) != `{"properties":{"value":{"type":"string"}},"type":"object"}` {
		t.Fatalf("expected canonical output schema, got %s", manifest.OutputSchema)
	}
}

func TestAppRegistry_InvalidSchemaRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.get",
		InputSchema:  json.RawMessage(`["not","an","object"]`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
	})
	if err == nil {
		t.Fatal("expected invalid schema to fail")
	}
	if !strings.Contains(err.Error(), `invalid input_schema`) {
		t.Fatalf("expected invalid input_schema error, got %v", err)
	}
	if !strings.Contains(err.Error(), `must be a JSON object`) {
		t.Fatalf("expected object-shape error, got %v", err)
	}
}

func TestAppRegistry_LegacyVerifyEndpointMapsToVerifyPrimitive(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:          "kv-app",
		Name:           "kv.set",
		InputSchema:    json.RawMessage(`{"type":"object"}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
		SocketPath:     "/tmp/kv.sock",
		VerifyEndpoint: "kv.get",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	manifest, err := registry.Get(context.Background(), "kv.set")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if manifest == nil || manifest.Verify == nil {
		t.Fatalf("expected normalized verify declaration, got %+v", manifest)
	}
	if manifest.Verify.Strategy != "primitive" || manifest.Verify.Primitive != "kv.get" {
		t.Fatalf("unexpected verify normalization: %+v", manifest.Verify)
	}
	if manifest.VerifyEndpoint != "kv.get" {
		t.Fatalf("expected legacy verify_endpoint to remain populated, got %q", manifest.VerifyEndpoint)
	}
}

func TestAppRegistry_ExplicitPrimitiveVerifyMirrorsLegacyEndpoint(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.import",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
		Verify: &AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "kv.verify",
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	manifest, err := registry.Get(context.Background(), "kv.import")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if manifest == nil || manifest.Verify == nil {
		t.Fatalf("expected manifest verify, got %+v", manifest)
	}
	if manifest.VerifyEndpoint != "kv.verify" {
		t.Fatalf("expected primitive verify to mirror verify_endpoint, got %q", manifest.VerifyEndpoint)
	}
}

func TestAppRegistry_LegacyRollbackEndpointMapsToRollbackPrimitive(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:            "kv-app",
		Name:             "kv.set",
		InputSchema:      json.RawMessage(`{"type":"object"}`),
		OutputSchema:     json.RawMessage(`{"type":"object"}`),
		SocketPath:       "/tmp/kv.sock",
		RollbackEndpoint: "kv.rollback_set",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	manifest, err := registry.Get(context.Background(), "kv.set")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if manifest == nil || manifest.Rollback == nil {
		t.Fatalf("expected normalized rollback declaration, got %+v", manifest)
	}
	if manifest.Rollback.Strategy != "primitive" || manifest.Rollback.Primitive != "kv.rollback_set" {
		t.Fatalf("unexpected rollback normalization: %+v", manifest.Rollback)
	}
	if manifest.RollbackEndpoint != "kv.rollback_set" {
		t.Fatalf("expected legacy rollback_endpoint to remain populated, got %q", manifest.RollbackEndpoint)
	}
}

func TestAppRegistry_ExplicitPrimitiveRollbackMirrorsLegacyEndpoint(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.import",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
		Rollback: &AppPrimitiveRollback{
			Strategy:  "primitive",
			Primitive: "kv.rollback_import",
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	manifest, err := registry.Get(context.Background(), "kv.import")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if manifest == nil || manifest.Rollback == nil {
		t.Fatalf("expected manifest rollback, got %+v", manifest)
	}
	if manifest.RollbackEndpoint != "kv.rollback_import" {
		t.Fatalf("expected primitive rollback to mirror rollback_endpoint, got %q", manifest.RollbackEndpoint)
	}
}

func TestAppRegistry_InvalidVerifyDeclarationRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.set",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
		Verify: &AppPrimitiveVerify{
			Strategy: "command",
		},
	})
	if err == nil {
		t.Fatal("expected invalid verify declaration to fail")
	}
	if !strings.Contains(err.Error(), `verify.strategy "command" requires verify.command`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppRegistry_InvalidRollbackDeclarationRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	err := registry.Register(context.Background(), AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.set",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/kv.sock",
		Rollback: &AppPrimitiveRollback{
			Strategy: "primitive",
		},
	})
	if err == nil {
		t.Fatal("expected invalid rollback declaration to fail")
	}
	if !strings.Contains(err.Error(), `rollback.strategy "primitive" requires rollback.primitive`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
