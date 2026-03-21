package main

import (
	"encoding/json"
	"testing"
)

func resetState(current, previous string) {
	state.mu.Lock()
	state.current = current
	state.previous = previous
	state.mu.Unlock()
}

func TestDispatch_Set(t *testing.T) {
	resetState("", "")

	resp := dispatch(rpcRequest{ID: 1, Method: "demo.set", Params: []byte(`{"value":"hello"}`)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	state.mu.Lock()
	cur := state.current
	state.mu.Unlock()
	if cur != "hello" {
		t.Fatalf("expected current=hello, got %q", cur)
	}
}

func TestDispatch_Set_StoresPrevious(t *testing.T) {
	resetState("old", "")

	dispatch(rpcRequest{ID: 2, Method: "demo.set", Params: []byte(`{"value":"new"}`)})

	state.mu.Lock()
	cur, prev := state.current, state.previous
	state.mu.Unlock()
	if cur != "new" {
		t.Fatalf("expected current=new, got %q", cur)
	}
	if prev != "old" {
		t.Fatalf("expected previous=old, got %q", prev)
	}
}

func TestDispatch_VerifySet_Pass(t *testing.T) {
	resetState("v1", "")

	resp := dispatch(rpcRequest{ID: 3, Method: "demo.verify_set", Params: []byte(`{}`)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"] != true {
		t.Fatalf("expected passed=true for non-FAIL_VERIFY value, got %v", result["passed"])
	}
}

func TestDispatch_VerifySet_Fail(t *testing.T) {
	resetState("FAIL_VERIFY", "")

	resp := dispatch(rpcRequest{ID: 4, Method: "demo.verify_set", Params: []byte(`{}`)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"] != false {
		t.Fatalf("expected passed=false for FAIL_VERIFY value, got %v", result["passed"])
	}
}

func TestDispatch_RollbackSet(t *testing.T) {
	resetState("v2", "v1")

	resp := dispatch(rpcRequest{ID: 5, Method: "demo.rollback_set", Params: []byte(`{}`)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	state.mu.Lock()
	cur := state.current
	state.mu.Unlock()
	if cur != "v1" {
		t.Fatalf("expected rollback to v1, got %q", cur)
	}
}

func TestDispatch_State(t *testing.T) {
	resetState("myvalue", "")

	resp := dispatch(rpcRequest{ID: 6, Method: "demo.state", Params: []byte(`{}`)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["value"] != "myvalue" {
		t.Fatalf("expected value=myvalue, got %v", result["value"])
	}
}

func TestDispatch_Fail(t *testing.T) {
	resp := dispatch(rpcRequest{ID: 7, Method: "demo.fail", Params: []byte(`{}`)})
	if resp.Error == nil {
		t.Fatal("expected error response from demo.fail")
	}
	if resp.Error.Code == 0 {
		t.Fatal("expected non-zero error code")
	}
}

func TestDispatch_UnknownMethod(t *testing.T) {
	resp := dispatch(rpcRequest{ID: 8, Method: "demo.unknown", Params: []byte(`{}`)})
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestDispatch_Set_InvalidParams(t *testing.T) {
	resp := dispatch(rpcRequest{ID: 9, Method: "demo.set", Params: []byte(`not-json`)})
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
}
