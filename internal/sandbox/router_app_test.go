package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"primitivebox/internal/primitive"
)

func TestAppRouter_AppPrimitive_NotFound(t *testing.T) {
	t.Parallel()

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(primitive.NewInMemoryAppRegistry())

	_, err := router.Route(context.Background(), "myapp.unknown", json.RawMessage(`{}`))
	if !errors.Is(err, ErrPrimitiveNotFound) {
		t.Fatalf("expected ErrPrimitiveNotFound, got %v", err)
	}
}

func TestAppRouter_AppPrimitive_Success(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	socketPath := shortSocketPath(t)
	startTestAppSocket(t, socketPath, func(req map[string]any) map[string]any {
		if req["method"] != "myapp.greet" {
			t.Fatalf("unexpected method: %#v", req["method"])
		}
		params, ok := req["params"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected params: %#v", req["params"])
		}
		return map[string]any{
			"id":     req["id"],
			"result": map[string]any{"message": "hello " + params["name"].(string)},
			"error":  nil,
		}
	})

	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
		SocketPath:   socketPath,
	}); err != nil {
		t.Fatalf("register manifest: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	result, err := router.Route(context.Background(), "myapp.greet", json.RawMessage(`{"name":"world"}`))
	if err != nil {
		t.Fatalf("route app primitive: %v", err)
	}

	payload, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result.Data)
	}
	if payload["message"] != "hello world" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAppRouter_AppPrimitive_SocketUnreachable(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   "/tmp/primitivebox-no-such-socket.sock",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err := router.Route(context.Background(), "myapp.greet", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when socket is unreachable")
	}
	pe, ok := err.(*primitive.PrimitiveError)
	if !ok {
		t.Fatalf("expected PrimitiveError, got %T", err)
	}
	if pe.Message != "adapter myapp is unavailable" {
		t.Fatalf("unexpected unavailable message: %s", pe.Message)
	}
	got, err := registry.Get(context.Background(), "myapp.greet")
	if err != nil {
		t.Fatalf("get manifest after dial failure: %v", err)
	}
	if got == nil || got.Availability != primitive.AppPrimitiveUnavailable {
		t.Fatalf("expected manifest to be marked unavailable, got %+v", got)
	}

	_, err = router.Route(context.Background(), "myapp.greet", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected cached unavailable error on second call")
	}
	pe, ok = err.(*primitive.PrimitiveError)
	if !ok {
		t.Fatalf("expected PrimitiveError on second call, got %T", err)
	}
	if pe.Message != "adapter myapp is unavailable" {
		t.Fatalf("unexpected cached unavailable message: %s", pe.Message)
	}
}

func TestAppRouter_AppPrimitive_RPCError(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	socketPath := shortSocketPath(t)
	startTestAppSocket(t, socketPath, func(req map[string]any) map[string]any {
		return map[string]any{
			"id":     req["id"],
			"result": nil,
			"error":  map[string]any{"code": -1, "message": "not authorized"},
		}
	})

	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err := router.Route(context.Background(), "myapp.greet", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from app RPC error response")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("expected error to contain 'not authorized', got: %v", err)
	}
}

func TestAppRouter_AppPrimitive_MalformedResponse(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	socketPath := shortSocketPath(t)

	// Start a socket that writes invalid JSON.
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		skipIfListenUnavailable(t, err)
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the request.
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		// Reply with garbage.
		_, _ = conn.Write([]byte("not json at all\n"))
	}()

	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:        "myapp",
		Name:         "myapp.greet",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err = router.Route(context.Background(), "myapp.greet", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from malformed response")
	}
}

func TestAppRouter_AppPrimitive_VerifyReceivesOriginalParams(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	socketPath := shortSocketPath(t)
	var calls []map[string]any
	startTestAppSocket(t, socketPath, func(req map[string]any) map[string]any {
		calls = append(calls, req)
		switch req["method"] {
		case "myapp.mutate":
			return map[string]any{
				"id":     req["id"],
				"result": map[string]any{"ok": true},
				"error":  nil,
			}
		case "myapp.verify":
			return map[string]any{
				"id":     req["id"],
				"result": map[string]any{"passed": true},
				"error":  nil,
			}
		default:
			t.Fatalf("unexpected method: %#v", req["method"])
			return nil
		}
	})

	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:          "myapp",
		Name:           "myapp.mutate",
		InputSchema:    json.RawMessage(`{"type":"object"}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
		SocketPath:     socketPath,
		VerifyEndpoint: "myapp.verify",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err := router.Route(context.Background(), "myapp.mutate", json.RawMessage(`{"name":"demo","enabled":true}`))
	if err != nil {
		t.Fatalf("route app primitive: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 app RPC calls, got %d", len(calls))
	}
	for idx, method := range []string{"myapp.mutate", "myapp.verify"} {
		if calls[idx]["method"] != method {
			t.Fatalf("calls[%d].method = %#v, want %q", idx, calls[idx]["method"], method)
		}
		params, ok := calls[idx]["params"].(map[string]any)
		if !ok {
			t.Fatalf("calls[%d].params = %#v", idx, calls[idx]["params"])
		}
		if params["name"] != "demo" || params["enabled"] != true {
			t.Fatalf("calls[%d].params = %#v, want original params", idx, params)
		}
	}
}

func TestAppRouter_AppPrimitive_RollbackReceivesOriginalParamsOnVerifyFailure(t *testing.T) {
	t.Parallel()

	registry := primitive.NewInMemoryAppRegistry()
	socketPath := shortSocketPath(t)
	var calls []map[string]any
	startTestAppSocket(t, socketPath, func(req map[string]any) map[string]any {
		calls = append(calls, req)
		switch req["method"] {
		case "myapp.mutate":
			return map[string]any{
				"id":     req["id"],
				"result": map[string]any{"ok": true},
				"error":  nil,
			}
		case "myapp.verify":
			return map[string]any{
				"id":     req["id"],
				"result": map[string]any{"passed": false},
				"error":  nil,
			}
		case "myapp.rollback":
			return map[string]any{
				"id":     req["id"],
				"result": map[string]any{"rolled_back": true},
				"error":  nil,
			}
		default:
			t.Fatalf("unexpected method: %#v", req["method"])
			return nil
		}
	})

	if err := registry.Register(context.Background(), primitive.AppPrimitiveManifest{
		AppID:            "myapp",
		Name:             "myapp.mutate",
		InputSchema:      json.RawMessage(`{"type":"object"}`),
		OutputSchema:     json.RawMessage(`{"type":"object"}`),
		SocketPath:       socketPath,
		VerifyEndpoint:   "myapp.verify",
		RollbackEndpoint: "myapp.rollback",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err := router.Route(context.Background(), "myapp.mutate", json.RawMessage(`{"name":"demo","enabled":true}`))
	if err == nil || !strings.Contains(err.Error(), "app_primitive_verify_failed") {
		t.Fatalf("expected verify failure, got %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("expected 3 app RPC calls, got %d", len(calls))
	}
	for idx, method := range []string{"myapp.mutate", "myapp.verify", "myapp.rollback"} {
		if calls[idx]["method"] != method {
			t.Fatalf("calls[%d].method = %#v, want %q", idx, calls[idx]["method"], method)
		}
		params, ok := calls[idx]["params"].(map[string]any)
		if !ok {
			t.Fatalf("calls[%d].params = %#v", idx, calls[idx]["params"])
		}
		if idx < 2 {
			if params["name"] != "demo" || params["enabled"] != true {
				t.Fatalf("calls[%d].params = %#v, want original params", idx, params)
			}
			continue
		}
		rollbackParams, ok := params["params"].(map[string]any)
		if !ok {
			t.Fatalf("rollback params = %#v", params)
		}
		if rollbackParams["name"] != "demo" || rollbackParams["enabled"] != true {
			t.Fatalf("rollback params = %#v, want original params nested under params", rollbackParams)
		}
		if params["primitive"] != "myapp.mutate" {
			t.Fatalf("rollback envelope = %#v, want primitive name", params)
		}
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()

	file, err := os.CreateTemp("", "pb-app-*.sock")
	if err != nil {
		t.Fatalf("create temp socket path: %v", err)
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}

func startTestAppSocket(t *testing.T, socketPath string, handler func(map[string]any) map[string]any) {
	t.Helper()

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		skipIfListenUnavailable(t, err)
		t.Fatalf("listen on unix socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			func() {
				defer conn.Close()

				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req map[string]any
				if err := json.Unmarshal(line, &req); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				resp := handler(req)
				data, err := json.Marshal(resp)
				if err != nil {
					t.Errorf("encode response: %v", err)
					return
				}
				_, _ = conn.Write(append(data, '\n'))
			}()
		}
	}()
}

func skipIfListenUnavailable(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "bind: operation not permitted") {
		t.Skipf("skipping test: unix socket listen unavailable in current environment: %v", err)
	}
}
