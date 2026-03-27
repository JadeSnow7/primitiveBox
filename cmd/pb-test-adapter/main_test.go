package main

import (
	"context"
	"encoding/json"
	"testing"
)

func newTestState(value string) *adapterState {
	return &adapterState{value: value}
}

func TestDispatchEcho(t *testing.T) {
	result, rpcErr := dispatch(context.Background(), newTestState(""), "demo.echo", json.RawMessage(`{"message":"hello"}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["message"] != "hello" {
		t.Fatalf("expected echoed message, got %#v", payload)
	}
}

func TestDispatchSetStoresPrevious(t *testing.T) {
	state := newTestState("old")

	result, rpcErr := dispatch(context.Background(), state, "demo.set", json.RawMessage(`{"value":"new"}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["previous_value"] != "old" {
		t.Fatalf("expected previous value to be reported, got %#v", payload)
	}
	if state.value != "new" {
		t.Fatalf("expected state value to update, got %q", state.value)
	}
}

func TestDispatchVerifySetPassesWhenStateMatches(t *testing.T) {
	result, rpcErr := dispatch(context.Background(), newTestState("v1"), "demo.verify_set", json.RawMessage(`{"value":"v1"}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["passed"] != true {
		t.Fatalf("expected passed=true, got %#v", payload)
	}
}

func TestDispatchVerifySetFailsWhenRequested(t *testing.T) {
	result, rpcErr := dispatch(context.Background(), newTestState("v1"), "demo.verify_set", json.RawMessage(`{"value":"v1","verify_fail":true}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["passed"] != false {
		t.Fatalf("expected passed=false, got %#v", payload)
	}
}

func TestDispatchRollbackSetRestoresPreviousValue(t *testing.T) {
	state := newTestState("v2")

	result, rpcErr := dispatch(context.Background(), state, "demo.rollback_set", json.RawMessage(`{"result":{"previous_value":"v1"}}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["rolled_back"] != true || state.value != "v1" {
		t.Fatalf("expected rollback to restore v1, got payload=%#v state=%q", payload, state.value)
	}
}

func TestDispatchState(t *testing.T) {
	result, rpcErr := dispatch(context.Background(), newTestState("myvalue"), "demo.state", json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	if payload["value"] != "myvalue" {
		t.Fatalf("expected state payload, got %#v", payload)
	}
}

func TestDispatchFail(t *testing.T) {
	_, rpcErr := dispatch(context.Background(), newTestState(""), "demo.fail", json.RawMessage(`{"reason":"boom"}`))
	if rpcErr == nil {
		t.Fatal("expected deliberate failure")
	}
	if rpcErr.Code == 0 {
		t.Fatal("expected non-zero error code")
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	_, rpcErr := dispatch(context.Background(), newTestState(""), "demo.unknown", json.RawMessage(`{}`))
	if rpcErr == nil {
		t.Fatal("expected unknown method error")
	}
}

func TestDispatchSetInvalidParams(t *testing.T) {
	_, rpcErr := dispatch(context.Background(), newTestState(""), "demo.set", json.RawMessage(`not-json`))
	if rpcErr == nil {
		t.Fatal("expected invalid params error")
	}
}
