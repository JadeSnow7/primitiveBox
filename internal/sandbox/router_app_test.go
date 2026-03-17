package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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
		AppID:      "myapp",
		Name:       "myapp.greet",
		SocketPath: socketPath,
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
		AppID:      "myapp",
		Name:       "myapp.greet",
		SocketPath: "/tmp/primitivebox-no-such-socket.sock",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	router := NewRouter(primitive.NewRegistry())
	router.RegisterAppRegistry(registry)

	_, err := router.Route(context.Background(), "myapp.greet", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when socket is unreachable")
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
		AppID:      "myapp",
		Name:       "myapp.greet",
		SocketPath: socketPath,
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
		AppID:      "myapp",
		Name:       "myapp.greet",
		SocketPath: socketPath,
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

func shortSocketPath(t *testing.T) string {
	t.Helper()

	path := fmt.Sprintf("/tmp/primitivebox-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}

func startTestAppSocket(t *testing.T, socketPath string, handler func(map[string]any) map[string]any) {
	t.Helper()

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
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
