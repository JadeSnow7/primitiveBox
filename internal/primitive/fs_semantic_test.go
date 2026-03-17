package primitive_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtime"
)

func TestWriteFile_GoFile_SymbolChangesPresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package sample\n\nfunc Foo(x int) error { return nil }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	intent := &cvr.PrimitiveIntent{}
	ctx := context.WithValue(context.Background(), runtime.IntentContextKey, intent)
	result, err := primitive.NewFSWrite(dir).Execute(ctx, mustJSON(t, map[string]any{
		"path":    "main.go",
		"mode":    "overwrite",
		"content": "package sample\n\nimport \"context\"\n\nfunc Foo(ctx context.Context, x int) error { return nil }\n",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	payload := decodeResultMap(t, result.Data)
	changes, ok := payload["symbol_changes"].([]any)
	if !ok || len(changes) == 0 {
		t.Fatalf("expected symbol_changes, got %#v", payload["symbol_changes"])
	}
	if !containsString(intent.AffectedScopes, "sample.Foo") {
		t.Fatalf("expected affected scope sample.Foo, got %v", intent.AffectedScopes)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func decodeResultMap(t *testing.T, value any) map[string]any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
