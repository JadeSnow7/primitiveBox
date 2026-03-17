package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
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
