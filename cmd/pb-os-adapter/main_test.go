package main

import (
	"context"
	"encoding/json"
	"syscall"
	"testing"
	"time"

	"primitivebox/internal/cvr"
)

func TestProcessRegistryWaitAndSignalLifecycle(t *testing.T) {
	registry := newProcessRegistry()

	record, err := registry.spawn([]string{"sleep", "5"}, "", nil)
	if err != nil {
		t.Fatalf("spawn process: %v", err)
	}
	if record.processID == "" {
		t.Fatal("expected process_id to be set")
	}
	if record.pid <= 0 {
		t.Fatalf("expected pid > 0, got %d", record.pid)
	}

	select {
	case <-record.doneCh:
		t.Fatal("expected process to still be running")
	case <-time.After(100 * time.Millisecond):
	}

	waitTimedOut := record.waitResult(true)
	if waitTimedOut["timed_out"] != true {
		t.Fatalf("expected timed_out=true, got %#v", waitTimedOut)
	}
	if waitTimedOut["running"] != true {
		t.Fatalf("expected running=true before signal, got %#v", waitTimedOut)
	}

	sent, err := record.sendSignal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("send signal: %v", err)
	}
	if !sent {
		t.Fatal("expected SIGTERM to be sent")
	}

	select {
	case <-record.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after SIGTERM")
	}

	result := record.waitResult(false)
	if result["exited"] != true {
		t.Fatalf("expected exited=true, got %#v", result)
	}
	if result["running"] != false {
		t.Fatalf("expected running=false, got %#v", result)
	}

	sentAgain, err := record.sendSignal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("send signal after exit: %v", err)
	}
	if sentAgain {
		t.Fatal("expected no signal to be sent after exit")
	}
}

func TestProcessRegistryUnknownProcessID(t *testing.T) {
	registry := newProcessRegistry()
	if _, ok := registry.get("proc-missing"); ok {
		t.Fatal("expected missing process_id lookup to fail")
	}
}

// ---------------------------------------------------------------------------
// service.* and pkg.* manifest and dispatch tests
// ---------------------------------------------------------------------------

func TestBuildManifestSetServiceAndPkgFamilies(t *testing.T) {
	manifests := buildManifestSet("test-app", "/tmp/test.sock")
	byName := make(map[string]interface{}, len(manifests))
	for _, m := range manifests {
		byName[m.Name] = m
	}

	// Verify all 13 primitives are declared.
	expected := []string{
		"process.list", "process.spawn", "process.wait", "process.terminate", "process.kill",
		"service.status", "service.start", "service.stop", "service.restart",
		"pkg.list", "pkg.install", "pkg.remove", "pkg.verify",
	}
	for _, name := range expected {
		if _, ok := byName[name]; !ok {
			t.Errorf("manifest missing: %s", name)
		}
	}
	if t.Failed() {
		return
	}

	// Retrieve typed manifests.
	getMf := func(name string) interface{} { return byName[name] }
	_ = getMf

	// Re-build as typed slice for intent access.
	mfByName := make(map[string]interface{})
	for i := range manifests {
		mfByName[manifests[i].Name] = &manifests[i]
	}

	type mf = interface{ GetName() string }

	// service.status — query intent, no verify/rollback.
	for i := range manifests {
		if manifests[i].Name != "service.status" {
			continue
		}
		if manifests[i].Intent.Category != cvr.IntentQuery {
			t.Errorf("service.status intent.category: got %q, want %q", manifests[i].Intent.Category, cvr.IntentQuery)
		}
		if manifests[i].VerifyEndpoint != "" {
			t.Errorf("service.status should have no verify_endpoint, got %q", manifests[i].VerifyEndpoint)
		}
		if manifests[i].RollbackEndpoint != "" {
			t.Errorf("service.status should have no rollback_endpoint, got %q", manifests[i].RollbackEndpoint)
		}
	}

	// service.start — mutation/medium, verify=service.status.
	for i := range manifests {
		if manifests[i].Name != "service.start" {
			continue
		}
		if manifests[i].Intent.Category != cvr.IntentMutation {
			t.Errorf("service.start intent.category: got %q, want %q", manifests[i].Intent.Category, cvr.IntentMutation)
		}
		if manifests[i].VerifyEndpoint != "service.status" {
			t.Errorf("service.start verify_endpoint: got %q, want %q", manifests[i].VerifyEndpoint, "service.status")
		}
	}

	// service.stop — mutation/medium, rollback=service.start.
	for i := range manifests {
		if manifests[i].Name != "service.stop" {
			continue
		}
		if manifests[i].Intent.Category != cvr.IntentMutation {
			t.Errorf("service.stop intent.category: got %q, want %q", manifests[i].Intent.Category, cvr.IntentMutation)
		}
		if manifests[i].RollbackEndpoint != "service.start" {
			t.Errorf("service.stop rollback_endpoint: got %q, want %q", manifests[i].RollbackEndpoint, "service.start")
		}
	}

	// pkg.install — mutation/high, reversible=false, verify=pkg.verify, rollback=pkg.remove.
	for i := range manifests {
		if manifests[i].Name != "pkg.install" {
			continue
		}
		if manifests[i].Intent.Category != cvr.IntentMutation {
			t.Errorf("pkg.install intent.category: got %q, want %q", manifests[i].Intent.Category, cvr.IntentMutation)
		}
		if manifests[i].Intent.RiskLevel != cvr.RiskHigh {
			t.Errorf("pkg.install risk_level: got %q, want %q", manifests[i].Intent.RiskLevel, cvr.RiskHigh)
		}
		if manifests[i].Intent.Reversible {
			t.Error("pkg.install must be irreversible (Reversible=false triggers checkpoint)")
		}
		if manifests[i].VerifyEndpoint != "pkg.verify" {
			t.Errorf("pkg.install verify_endpoint: got %q, want %q", manifests[i].VerifyEndpoint, "pkg.verify")
		}
		if manifests[i].RollbackEndpoint != "pkg.remove" {
			t.Errorf("pkg.install rollback_endpoint: got %q, want %q", manifests[i].RollbackEndpoint, "pkg.remove")
		}
	}

	// pkg.verify — query intent.
	for i := range manifests {
		if manifests[i].Name != "pkg.verify" {
			continue
		}
		if manifests[i].Intent.Category != cvr.IntentQuery {
			t.Errorf("pkg.verify intent.category: got %q, want %q", manifests[i].Intent.Category, cvr.IntentQuery)
		}
	}
}

func TestDispatchServiceStatusRequiresName(t *testing.T) {
	ctx := context.Background()
	state := &adapterState{registry: newProcessRegistry()}
	_, rpcErr := dispatch(ctx, state, "service.status", json.RawMessage(`{}`))
	if rpcErr == nil {
		t.Fatal("expected error for missing name")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("expected code -32602, got %d", rpcErr.Code)
	}
}

func TestDispatchPkgListRejectsExtraParams(t *testing.T) {
	ctx := context.Background()
	state := &adapterState{registry: newProcessRegistry()}
	_, rpcErr := dispatch(ctx, state, "pkg.list", json.RawMessage(`{"unexpected":true}`))
	if rpcErr == nil {
		t.Fatal("expected error for unexpected params")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("expected code -32602, got %d", rpcErr.Code)
	}
}

func TestHandleServiceStatusIncludesPassedFlag(t *testing.T) {
	t.Parallel()

	result, rpcErr := handleServiceStatus(context.Background(), "primitivebox-missing-service")
	if rpcErr != nil {
		t.Fatalf("handleServiceStatus returned rpc error: %v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	passed, ok := payload["passed"].(bool)
	if !ok {
		t.Fatalf("expected passed flag in payload, got %#v", payload)
	}
	running, ok := payload["running"].(bool)
	if !ok {
		t.Fatalf("expected running flag in payload, got %#v", payload)
	}
	if passed != running {
		t.Fatalf("expected passed to mirror running, got payload %#v", payload)
	}
}

func TestHandlePkgVerifyIncludesPassedFlag(t *testing.T) {
	t.Parallel()

	result, rpcErr := handlePkgVerify(context.Background(), "primitivebox-missing-package")
	if rpcErr != nil {
		t.Fatalf("handlePkgVerify returned rpc error: %v", rpcErr)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", result)
	}
	passed, ok := payload["passed"].(bool)
	if !ok {
		t.Fatalf("expected passed flag in payload, got %#v", payload)
	}
	verified, ok := payload["verified"].(bool)
	if !ok {
		t.Fatalf("expected verified flag in payload, got %#v", payload)
	}
	if passed != verified {
		t.Fatalf("expected passed to mirror verified, got payload %#v", payload)
	}
}

func TestIsLinuxReturnsBool(t *testing.T) {
	// Verifies the platform detection helper compiles and does not panic.
	result := isLinux()
	_ = result // value depends on runtime.GOOS — just ensure it's reachable
}

func TestDispatchUnknownServiceMethodFails(t *testing.T) {
	ctx := context.Background()
	state := &adapterState{registry: newProcessRegistry()}
	_, rpcErr := dispatch(ctx, state, "service.nonexistent", json.RawMessage(`{}`))
	if rpcErr == nil || rpcErr.Code != -32601 {
		t.Fatalf("expected -32601 method not found, got %v", rpcErr)
	}
}
