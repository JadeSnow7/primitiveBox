package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"primitivebox/internal/primitive"
)

func TestUIRootServesEmbeddedIndex(t *testing.T) {
	t.Parallel()

	server := NewServer(primitive.NewRegistry(), nil, nil)
	server.AttachUI(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>inspector</body></html>")},
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "inspector") {
		t.Fatalf("expected embedded index html, got %s", resp.Body.String())
	}
}

func TestUISPAFallbackDoesNotInterceptRPC(t *testing.T) {
	t.Parallel()

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(t.TempDir(), primitive.DefaultOptions())

	server := NewServer(registry, nil, nil)
	server.AttachUI(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>inspector</body></html>")},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"fs.list","params":{"path":"."},"id":"1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected rpc route to stay intact, got %d: %s", resp.Code, resp.Body.String())
	}
}
