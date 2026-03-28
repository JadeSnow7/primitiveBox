package goal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"primitivebox/internal/orchestrator"
	"primitivebox/internal/primitive"
	"primitivebox/internal/sandbox"
)

func TestRouterExecutorAdapter_SuccessfulCall(t *testing.T) {
	t.Parallel()

	reg := primitive.NewRegistry()
	if err := reg.Register(&echoPrimitive{response: map[string]any{"pong": true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := sandbox.NewRouter(reg)
	adapter := NewRouterExecutorAdapter(router)

	result, err := adapter.Execute(context.Background(), "echo.ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success=true")
	}
	if len(result.Data) == 0 {
		t.Errorf("expected non-empty data")
	}
}

func TestRouterExecutorAdapter_PrimitiveNotFound(t *testing.T) {
	t.Parallel()

	reg := primitive.NewRegistry()
	router := sandbox.NewRouter(reg)
	adapter := NewRouterExecutorAdapter(router)

	result, err := adapter.Execute(context.Background(), "unknown.method", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown primitive")
	}
	if result == nil {
		t.Fatal("expected StepResult even on error")
	}
	if result.Success {
		t.Errorf("expected success=false on route error")
	}
}

func TestRouterExecutorAdapter_ListPrimitivesReturnsNil(t *testing.T) {
	t.Parallel()

	adapter := &RouterExecutorAdapter{router: sandbox.NewRouter(nil)}
	if prims := adapter.ListPrimitives(); prims != nil {
		t.Errorf("expected nil, got %v", prims)
	}
}

func TestRouterExecutorAdapter_PrimitiveReturnsError(t *testing.T) {
	t.Parallel()

	reg := primitive.NewRegistry()
	if err := reg.Register(&errorPrimitive{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := sandbox.NewRouter(reg)
	adapter := NewRouterExecutorAdapter(router)

	result, err := adapter.Execute(context.Background(), "error.prim", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from failing primitive")
	}
	if result.Success {
		t.Errorf("expected success=false")
	}
	if result.Error == nil {
		t.Errorf("expected StepError to be set")
	}
}

// Ensure RouterExecutorAdapter satisfies the orchestrator.PrimitiveExecutor interface at compile time.
var _ orchestrator.PrimitiveExecutor = (*RouterExecutorAdapter)(nil)

// ── Test doubles ──────────────────────────────────────────────────────────────

// echoPrimitive is a minimal test double that satisfies primitive.Primitive.
type echoPrimitive struct {
	response map[string]any
}

func (e *echoPrimitive) Name() string     { return "echo.ping" }
func (e *echoPrimitive) Category() string { return "echo" }
func (e *echoPrimitive) Schema() primitive.Schema {
	return primitive.Schema{Name: "echo.ping"}
}
func (e *echoPrimitive) Execute(_ context.Context, _ json.RawMessage) (primitive.Result, error) {
	return primitive.Result{Data: e.response, Duration: 1}, nil
}

// errorPrimitive always returns an error.
type errorPrimitive struct{}

func (ep *errorPrimitive) Name() string     { return "error.prim" }
func (ep *errorPrimitive) Category() string { return "error" }
func (ep *errorPrimitive) Schema() primitive.Schema {
	return primitive.Schema{Name: "error.prim"}
}
func (ep *errorPrimitive) Execute(_ context.Context, _ json.RawMessage) (primitive.Result, error) {
	return primitive.Result{}, errors.New("primitive exploded")
}
