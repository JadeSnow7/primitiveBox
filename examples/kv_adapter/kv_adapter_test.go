package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/eventing"
	"primitivebox/internal/orchestrator"
	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
	"primitivebox/internal/sandbox"
)

const testSandboxID = "sb-kv-validation"

var (
	buildOnce         sync.Once
	builtAdapterPath  string
	buildAdapterError error
)

type validationEnv struct {
	t             *testing.T
	workspace     string
	appRegistry   primitive.AppPrimitiveRegistry
	sandboxServer *rpc.Server
	sandboxURL    string
	sandboxEvents *memoryEventStore
	hostServer    *rpc.Server
	hostURL       string
	hostEvents    *memoryEventStore
	manager       *sandbox.Manager
}

type adapterProcess struct {
	t          *testing.T
	cmd        *exec.Cmd
	socketPath string
	dbPath     string
	stdout     bytes.Buffer
	stderr     bytes.Buffer
}

type memoryEventStore struct {
	mu     sync.Mutex
	events []eventing.Event
}

type manifestStore struct {
	mu        sync.Mutex
	manifests map[string]cvr.CheckpointManifest
}

type passthroughRuntimeDriver struct{}

type routerExecutor struct {
	router      *sandbox.Router
	appRegistry primitive.AppPrimitiveRegistry
	systemNames []string
}

type recordingExecutor struct {
	inner *routerExecutor
	mu    sync.Mutex
	calls []string
}

type fixedStrategy struct {
	result cvr.StrategyResult
}

func (s fixedStrategy) Name() string        { return "fixed" }
func (s fixedStrategy) Description() string { return "fixed test strategy" }
func (s fixedStrategy) Run(ctx context.Context, exec cvr.StrategyExecutor, result cvr.ExecuteResult, manifest *cvr.CheckpointManifest) (cvr.StrategyResult, error) {
	_ = ctx
	_ = exec
	_ = result
	_ = manifest
	return s.result, nil
}

func TestValidation(t *testing.T) {
	t.Run("Registration", func(t *testing.T) {
		t.Run("BasicAndSchema", testRegistrationBasicAndSchema)
		t.Run("Upsert", testRegistrationUpsert)
		t.Run("CrossAppConflict", testCrossAppConflict)
		t.Run("CrashLeavesStaleRoute", testCrashLeavesStaleRoute)
		t.Run("CrashReactivation", testCrashReactivation)
		t.Run("NamespaceIsolation", testNamespaceIsolation)
		t.Run("InvalidVerifyRejected", testRegistrationInvalidVerifyRejected)
		t.Run("MetadataRichness", testMetadataRichness)
	})

	t.Run("Dispatch", func(t *testing.T) {
		t.Run("BasicAndProxy", testDispatchBasicAndProxy)
		t.Run("ErrorPropagation", testDispatchErrorPropagation)
		t.Run("TimeoutAndLargePayload", testDispatchTimeoutAndLargePayload)
		t.Run("ConcurrentDispatch", testDispatchConcurrent)
		t.Run("BatchSetAtomicFailure", testDispatchBatchSetAtomicFailure)
		t.Run("BatchSetDuplicateKey", testDispatchBatchSetDuplicateKey)
		t.Run("CreateAtomic", testDispatchCreateAtomic)
		t.Run("SQLiteDefaultPathIsolation", testSQLiteDefaultPathIsolation)
		t.Run("SQLiteDefaultPathStable", testSQLiteDefaultPathStable)
		t.Run("StreamingUnsupported", testDispatchStreamingUnsupported)
	})

	t.Run("CVR", func(t *testing.T) {
		t.Run("CheckpointEnforcement", testCVRCheckpointEnforcement)
		t.Run("IrreversibleDecision", testCVRIrreversibleDecision)
		t.Run("LegacyVerifyEndpointTriggersVerification", testCVRLegacyVerifyEndpointTriggersVerification)
		t.Run("PrimitiveVerifyStrategy", testCVRPrimitiveVerifyStrategy)
		t.Run("CommandVerifyStrategy", testCVRCommandVerifyStrategy)
		t.Run("VerifyStrategyNone", testCVRVerifyStrategyNone)
		t.Run("VerifyFailureAffectsOutcome", testCVRVerifyFailureAffectsOutcome)
		t.Run("LegacyRollbackEndpointTriggersRollback", testCVRLegacyRollbackEndpointTriggersRollback)
		t.Run("DeclaredRollbackPreferredOverStateRestore", testCVRDeclaredRollbackPreferredOverStateRestore)
		t.Run("IrreversibleAppMutationWithoutRollbackFailsClosed", testCVRIrreversibleAppMutationWithoutRollbackFailsClosed)
		t.Run("RestoreDoesNotRollbackAdapterState", testCVRRestoreDoesNotRollbackAdapterState)
		t.Run("MacroSafeEditIsNotGeneric", testCVRMacroSafeEditNotGeneric)
		t.Run("RecoveryPolicy", testCVRRecoveryPolicy)
		t.Run("EventEmission", testCVREventEmission)
	})
}

func testRegistrationBasicAndSchema(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	items := env.mustListSandboxAppPrimitives(t)
	if len(items) != 10 {
		t.Fatalf("expected 10 app primitives, got %d", len(items))
	}
	if !containsManifest(items, "kv.get") || !containsManifest(items, "kv.verify") || !containsManifest(items, "kv.rollback_set") {
		t.Fatalf("expected kv.* primitives to be registered, got %+v", items)
	}

	system := env.mustListSystemPrimitives(t, env.sandboxURL)
	if !containsSystemPrimitive(system, "kv.get") {
		t.Fatalf("expected /primitives to include dynamic app primitives, got %+v", system)
	}
	systemByName := primitiveSchemaByName(system)
	if systemByName["kv.get"]["status"] != string(primitive.AppPrimitiveActive) {
		t.Fatalf("expected kv.get to be listed as active, got %+v", systemByName["kv.get"])
	}

	hostItems := env.mustListHostSandboxAppPrimitives(t)
	if len(hostItems) != 10 {
		t.Fatalf("expected host sandbox app-primitives proxy to list 10 entries, got %d", len(hostItems))
	}

	byName := manifestByName(items)
	if byName["kv.set"].Intent.Category != cvr.IntentMutation || byName["kv.set"].Intent.RiskLevel != cvr.RiskMedium || !byName["kv.set"].Intent.Reversible {
		t.Fatalf("unexpected kv.set intent: %+v", byName["kv.set"].Intent)
	}
	if byName["kv.delete"].Intent.Reversible || byName["kv.delete"].Intent.RiskLevel != cvr.RiskHigh {
		t.Fatalf("unexpected kv.delete intent: %+v", byName["kv.delete"].Intent)
	}
	if byName["kv.set"].VerifyEndpoint != "kv.verify" {
		t.Fatalf("expected verify_endpoint to round-trip, got %+v", byName["kv.set"])
	}
	if byName["kv.set"].Rollback == nil || byName["kv.set"].Rollback.Strategy != "primitive" || byName["kv.set"].Rollback.Primitive != "kv.rollback_set" {
		t.Fatalf("expected explicit rollback declaration for kv.set, got %+v", byName["kv.set"].Rollback)
	}
	if byName["kv.set"].RollbackEndpoint != "kv.rollback_set" {
		t.Fatalf("expected stored manifest to mirror explicit rollback into rollback_endpoint, got %+v", byName["kv.set"])
	}
	if byName["kv.delete"].RollbackEndpoint != "state.restore" {
		t.Fatalf("expected rollback_endpoint to round-trip, got %+v", byName["kv.delete"])
	}

	var batchSchema map[string]any
	if err := json.Unmarshal(byName["kv.batch_set"].InputSchema, &batchSchema); err != nil {
		t.Fatalf("decode kv.batch_set schema: %v", err)
	}
	props := batchSchema["properties"].(map[string]any)
	modeSchema := props["mode"].(map[string]any)
	if len(modeSchema["enum"].([]any)) != 2 {
		t.Fatalf("expected enum in kv.batch_set schema, got %#v", modeSchema)
	}
	entriesSchema := props["entries"].(map[string]any)
	if entriesSchema["type"] != "array" {
		t.Fatalf("expected array items schema, got %#v", entriesSchema)
	}
	entryItem := entriesSchema["items"].(map[string]any)
	entryProps := entryItem["properties"].(map[string]any)
	if _, ok := entryProps["metadata"]; !ok {
		t.Fatalf("expected nested object in batch entries, got %#v", entryItem)
	}
}

func testRegistrationUpsert(t *testing.T) {
	env := newValidationEnv(t)
	adapter1 := env.startAdapter(t, adapterStartOptions{})
	defer adapter1.Stop()

	done := make(chan error, 1)
	go func() {
		resp := env.callSandboxRPC(t, "kv.set", map[string]any{
			"key":   "inflight",
			"value": "first",
			"test_control": map[string]any{
				"delay_ms": 300,
			},
		})
		if resp.Error != nil {
			done <- errors.New(resp.Error.Message)
			return
		}
		done <- nil
	}()

	updated := env.loadManifestTemplate(t)
	for idx := range updated {
		if updated[idx].Name == "kv.get" {
			var schema map[string]any
			if err := json.Unmarshal(updated[idx].OutputSchema, &schema); err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			properties := schema["properties"].(map[string]any)
			properties["reregistered"] = map[string]any{"type": "boolean"}
			updated[idx].OutputSchema = mustJSON(schema)
		}
	}
	updatedManifestPath := writeManifestFile(t, t.TempDir(), updated)
	adapter2 := env.startAdapter(t, adapterStartOptions{
		socketPath: uniqueSocketPath(t, "adapter-v2"),
		manifest:   updatedManifestPath,
	})
	defer adapter2.Stop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("in-flight call failed during re-registration: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for in-flight call to complete")
	}

	var kvGet primitive.AppPrimitiveManifest
	waitFor(t, 5*time.Second, func() bool {
		items := env.mustListSandboxAppPrimitives(t)
		kvGet = manifestByName(items)["kv.get"]
		var schema map[string]any
		if err := json.Unmarshal(kvGet.OutputSchema, &schema); err != nil {
			return false
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return false
		}
		_, ok = properties["reregistered"]
		return ok && kvGet.SocketPath == adapter2.socketPath
	})

	resp := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "after-upsert", "value": "ok"})
	if resp.Error != nil {
		t.Fatalf("expected post-upsert adapter route to stay healthy, got %+v", resp.Error)
	}
}

func testCrossAppConflict(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	conflict := env.startAdapterExpectFailure(t, adapterStartOptions{
		appID:      "other-kv-app",
		socketPath: uniqueSocketPath(t, "adapter-conflict"),
	})
	if !strings.Contains(conflict, "app_primitive_conflict") {
		t.Fatalf("expected app_primitive_conflict, got %s", conflict)
	}
	if !strings.Contains(conflict, "kv.get") {
		t.Fatalf("expected conflict message to mention primitive name, got %s", conflict)
	}
	if !strings.Contains(conflict, defaultAppID) {
		t.Fatalf("expected conflict message to mention original app id, got %s", conflict)
	}
}

func testCrashLeavesStaleRoute(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	adapter.Kill()

	resp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "missing"})
	if resp.Error == nil {
		t.Fatal("expected kv.get to fail after adapter crash")
	}
	if resp.Error.Message != "adapter pb-kv-adapter is unavailable" {
		t.Fatalf("expected structured unavailable error, got %s", resp.Error.Message)
	}
	errorData, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected structured error data, got %#v", resp.Error.Data)
	}
	if errorData["app_id"] != defaultAppID {
		t.Fatalf("expected app_id in error data, got %#v", errorData)
	}

	items := env.mustListSandboxAppPrimitives(t)
	if !containsManifest(items, "kv.get") {
		t.Fatalf("expected dead adapter manifests to remain registered, got %+v", items)
	}
	if manifestByName(items)["kv.get"].Availability != primitive.AppPrimitiveUnavailable {
		t.Fatalf("expected dead adapter manifest to be marked unavailable, got %+v", manifestByName(items)["kv.get"])
	}

	primitiveList := env.mustListSystemPrimitives(t, env.sandboxURL)
	if primitiveSchemaByName(primitiveList)["kv.get"]["status"] != string(primitive.AppPrimitiveUnavailable) {
		t.Fatalf("expected /primitives to expose unavailable adapter status, got %+v", primitiveSchemaByName(primitiveList)["kv.get"])
	}
}

func testCrashReactivation(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	adapter.Kill()

	failed := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "missing"})
	if failed.Error == nil {
		t.Fatal("expected kv.get to fail after adapter crash")
	}

	restarted := env.startAdapter(t, adapterStartOptions{})
	defer restarted.Stop()
	waitFor(t, 5*time.Second, func() bool {
		items := env.mustListSandboxAppPrimitives(t)
		byName := manifestByName(items)
		return byName["kv.get"].Availability == primitive.AppPrimitiveActive
	})

	resp := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "after-restart", "value": "ok"})
	if resp.Error != nil {
		t.Fatalf("expected adapter call to succeed after re-registration, got %+v", resp.Error)
	}

	items := env.mustListSandboxAppPrimitives(t)
	byName := manifestByName(items)
	if byName["kv.get"].Availability != primitive.AppPrimitiveActive {
		t.Fatalf("expected kv.get to be active after reactivation, got %+v", byName["kv.get"])
	}
}

func testNamespaceIsolation(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	socketPath := uniqueSocketPath(t, "watch")
	watchServer := startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:     req.ID,
			Result: map[string]any{"watching": true},
		})
	})
	defer watchServer.Close()

	manifest := primitive.AppPrimitiveManifest{
		AppID:        "kv-watch-app",
		Name:         "kv.watch",
		Description:  "Watch keys in the shared namespace.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"watching":{"type":"boolean"}}}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	}
	registerManifest(t, env.sandboxURL, manifest)

	items := env.mustListSandboxAppPrimitives(t)
	if !containsManifest(items, "kv.get") || !containsManifest(items, "kv.watch") {
		t.Fatalf("expected same-namespace different-name registration to succeed, got %+v", items)
	}

	errResp := registerManifestExpectError(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "kv-watch-app",
		Name:         "fs.read",
		Description:  "Attempt to override reserved system primitive.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})
	if errResp.Error == nil {
		t.Fatal("expected reserved namespace registration to fail")
	}
	if !strings.Contains(errResp.Error.Message, "reserved system namespace") {
		t.Fatalf("expected reserved namespace error, got %+v", errResp)
	}
}

func testRegistrationInvalidVerifyRejected(t *testing.T) {
	env := newValidationEnv(t)

	errResp := registerManifestExpectError(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "kv-invalid-app",
		Name:         "kv.invalid_verify",
		Description:  "Invalid verify declaration.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   uniqueSocketPath(t, "invalid-verify"),
		Verify: &primitive.AppPrimitiveVerify{
			Strategy: "command",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})
	if errResp.Error == nil {
		t.Fatal("expected invalid verify declaration to fail")
	}
	if !strings.Contains(errResp.Error.Message, `verify.strategy "command" requires verify.command`) {
		t.Fatalf("unexpected verify validation error: %+v", errResp)
	}
}

func testMetadataRichness(t *testing.T) {
	manifestType := reflect.TypeOf(primitive.AppPrimitiveManifest{})
	if _, ok := manifestType.FieldByName("Description"); !ok {
		t.Fatal("expected description to be expressible")
	}
	if _, ok := manifestType.FieldByName("Verify"); !ok {
		t.Fatal("expected explicit verify contract to exist in current manifest")
	}
	if _, ok := manifestType.FieldByName("VerifyEndpoint"); !ok {
		t.Fatal("expected verify_endpoint to exist in current manifest")
	}
	if _, ok := manifestType.FieldByName("RollbackEndpoint"); !ok {
		t.Fatal("expected rollback_endpoint to exist in current manifest")
	}
	if _, ok := manifestType.FieldByName("Rollback"); !ok {
		t.Fatal("expected explicit rollback contract to exist in current manifest")
	}
	if _, ok := manifestType.FieldByName("Version"); ok {
		t.Fatal("did not expect version in current manifest type")
	}
	if _, ok := manifestType.FieldByName("TimeoutMs"); ok {
		t.Fatal("did not expect per-primitive timeout in current manifest type")
	}
	if _, ok := manifestType.FieldByName("Deprecated"); ok {
		t.Fatal("did not expect deprecation marker in current manifest type")
	}
}

func testDispatchBasicAndProxy(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	resp := env.callSandboxRPC(t, "kv.set", map[string]any{
		"key":   "alpha",
		"value": "one",
		"metadata": map[string]any{
			"content_type": "text/plain",
			"labels": map[string]string{
				"env": "test",
			},
			"tags":    []string{"seed"},
			"version": 1,
		},
	})
	if resp.Error != nil {
		t.Fatalf("sandbox-local kv.set failed: %+v", resp.Error)
	}
	payload := mustMap(t, resp.Result)
	if payload["stored"] != true || payload["key"] != "alpha" {
		t.Fatalf("unexpected kv.set payload: %#v", payload)
	}

	proxied := env.callHostSandboxRPC(t, "kv.get", map[string]any{"key": "alpha"})
	if proxied.Error != nil {
		t.Fatalf("host proxy kv.get failed: %+v", proxied.Error)
	}
	getPayload := mustMap(t, proxied.Result)
	if getPayload["value"] != "one" {
		t.Fatalf("unexpected host proxied kv.get payload: %#v", getPayload)
	}

	hostDirect := env.callRPC(t, env.hostURL+"/rpc", "kv.get", map[string]any{"key": "alpha"}, nil)
	if hostDirect.Error == nil || !strings.Contains(hostDirect.Error.Message, "unknown primitive") {
		t.Fatalf("expected host /rpc to not know kv.get, got %+v", hostDirect.Error)
	}
}

func testDispatchErrorPropagation(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	resp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "missing"})
	if resp.Error == nil {
		t.Fatal("expected missing key to fail")
	}
	if !strings.Contains(resp.Error.Message, "app_primitive_error: key not found: missing") {
		t.Fatalf("expected runtime-wrapped adapter error, got %s", resp.Error.Message)
	}
	if resp.Error.Code != rpc.CodeInternalError {
		t.Fatalf("expected internal error code after adapter error wrapping, got %d", resp.Error.Code)
	}
}

func testDispatchTimeoutAndLargePayload(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	router := sandbox.NewRouter(env.newSystemRegistry())
	router.RegisterAppRegistry(env.appRegistry)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := router.Route(ctx, "kv.set", mustJSON(map[string]any{
		"key":   "slow",
		"value": "value",
		"test_control": map[string]any{
			"delay_ms": 1000,
		},
	}))
	if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "deadline") && !strings.Contains(strings.ToLower(err.Error()), "timeout")) {
		t.Fatalf("expected caller deadline timeout from router, got %v", err)
	}

	large := strings.Repeat("x", 10*1024*1024)
	resp := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "blob", "value": large})
	if resp.Error != nil {
		t.Fatalf("expected 10MB kv.set to succeed, got %+v", resp.Error)
	}
	getResp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "blob"})
	if getResp.Error != nil {
		t.Fatalf("expected kv.get(blob) to succeed, got %+v", getResp.Error)
	}
	if len(mustMap(t, getResp.Result)["value"].(string)) != len(large) {
		t.Fatalf("expected round-trip large payload size=%d", len(large))
	}

	entries := make([]map[string]any, 0, 10000)
	for idx := 0; idx < 10000; idx++ {
		entries = append(entries, map[string]any{
			"key":   fmt.Sprintf("item-%05d", idx),
			"value": fmt.Sprintf("value-%05d", idx),
		})
	}
	importResp := env.callSandboxRPC(t, "kv.import", map[string]any{
		"mode":    "merge",
		"entries": entries,
	})
	if importResp.Error != nil {
		t.Fatalf("expected kv.import to succeed, got %+v", importResp.Error)
	}
	exportResp := env.callSandboxRPC(t, "kv.export", map[string]any{"format": "entries"})
	if exportResp.Error != nil {
		t.Fatalf("expected kv.export to succeed, got %+v", exportResp.Error)
	}
	exportPayload := mustMap(t, exportResp.Result)
	if int(exportPayload["entry_count"].(float64)) < 10000 {
		t.Fatalf("expected export entry_count >= 10000, got %#v", exportPayload)
	}
}

func testDispatchConcurrent(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 50)
	for idx := 0; idx < 50; idx++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := env.callSandboxRPC(t, "kv.set", map[string]any{
				"key":   fmt.Sprintf("concurrent-%02d", i),
				"value": fmt.Sprintf("value-%02d", i),
			})
			if resp.Error != nil {
				errCh <- fmt.Errorf("set %d failed: %s", i, resp.Error.Message)
			}
		}(idx)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	listResp := env.callSandboxRPC(t, "kv.list", map[string]any{"prefix": "concurrent-"})
	if listResp.Error != nil {
		t.Fatalf("expected kv.list to succeed, got %+v", listResp.Error)
	}
	items := mustMap(t, listResp.Result)["entries"].([]any)
	if len(items) != 50 {
		t.Fatalf("expected 50 concurrent entries, got %d", len(items))
	}
}

func testDispatchBatchSetAtomicFailure(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			env := newValidationEnv(t)
			adapter := env.startAdapter(t, adapterStartOptions{backend: backend})
			defer adapter.Stop()

			seedResp := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "existing", "value": "seed"})
			if seedResp.Error != nil {
				t.Fatalf("seed kv.set failed: %+v", seedResp.Error)
			}

			resp := env.callSandboxRPC(t, "kv.batch_set", map[string]any{
				"mode": "create",
				"entries": []map[string]any{
					{"key": "fresh-1", "value": "one"},
					{"key": "fresh-2", "value": "two"},
					{"key": "existing", "value": "overwrite"},
				},
			})
			if resp.Error == nil {
				t.Fatal("expected batch_set create conflict to fail")
			}
			if resp.Error.Code != rpc.CodeInternalError {
				t.Fatalf("expected wrapped internal error, got %+v", resp.Error)
			}
			if !strings.Contains(resp.Error.Message, "app_primitive_error: key already exists: existing") {
				t.Fatalf("unexpected batch_set error: %+v", resp.Error)
			}

			listResp := env.callSandboxRPC(t, "kv.list", map[string]any{"prefix": "fresh-"})
			if listResp.Error != nil {
				t.Fatalf("list fresh-* after failed batch: %+v", listResp.Error)
			}
			items := mustMap(t, listResp.Result)["entries"].([]any)
			if len(items) != 0 {
				t.Fatalf("expected failed batch to leave no fresh entries, got %#v", items)
			}

			getResp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "existing"})
			if getResp.Error != nil {
				t.Fatalf("expected existing key to remain readable, got %+v", getResp.Error)
			}
			if mustMap(t, getResp.Result)["value"] != "seed" {
				t.Fatalf("expected existing key to remain unchanged, got %#v", getResp.Result)
			}
		})
	}
}

func testDispatchBatchSetDuplicateKey(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			env := newValidationEnv(t)
			adapter := env.startAdapter(t, adapterStartOptions{backend: backend})
			defer adapter.Stop()

			resp := env.callSandboxRPC(t, "kv.batch_set", map[string]any{
				"mode": "create",
				"entries": []map[string]any{
					{"key": "dup-key", "value": "one"},
					{"key": "dup-key", "value": "two"},
				},
			})
			if resp.Error == nil {
				t.Fatal("expected duplicate key batch_set to fail")
			}
			if resp.Error.Code != rpc.CodeInternalError {
				t.Fatalf("expected wrapped internal error, got %+v", resp.Error)
			}
			if !strings.Contains(resp.Error.Message, "app_primitive_error: duplicate key in batch: dup-key") {
				t.Fatalf("unexpected duplicate key error: %+v", resp.Error)
			}

			listResp := env.callSandboxRPC(t, "kv.list", map[string]any{"prefix": "dup-key"})
			if listResp.Error != nil {
				t.Fatalf("list dup-key after failed batch: %+v", listResp.Error)
			}
			items := mustMap(t, listResp.Result)["entries"].([]any)
			if len(items) != 0 {
				t.Fatalf("expected duplicate-key batch to leave no writes, got %#v", items)
			}
		})
	}
}

func testDispatchCreateAtomic(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			env := newValidationEnv(t)
			adapter := env.startAdapter(t, adapterStartOptions{backend: backend})
			defer adapter.Stop()

			start := make(chan struct{})
			results := make(chan httpRPCResponse, 2)
			values := []string{"one", "two"}
			for _, value := range values {
				go func(v string) {
					<-start
					results <- env.callSandboxRPC(t, "kv.set", map[string]any{
						"key":   "create-race",
						"value": v,
						"mode":  "create",
					})
				}(value)
			}
			close(start)

			successes := 0
			conflicts := 0
			for range values {
				resp := <-results
				if resp.Error == nil {
					successes++
					continue
				}
				if resp.Error.Code == rpc.CodeInternalError && strings.Contains(resp.Error.Message, "app_primitive_error: key already exists: create-race") {
					conflicts++
					continue
				}
				t.Fatalf("unexpected create-race response: %+v", resp)
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("expected one success and one conflict, got successes=%d conflicts=%d", successes, conflicts)
			}

			getResp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "create-race"})
			if getResp.Error != nil {
				t.Fatalf("expected created key to exist, got %+v", getResp.Error)
			}
			value := mustMap(t, getResp.Result)["value"]
			if value != "one" && value != "two" {
				t.Fatalf("expected final value from one winning writer, got %#v", value)
			}
		})
	}
}

func testSQLiteDefaultPathIsolation(t *testing.T) {
	env := newValidationEnv(t)
	adapterA := env.startAdapter(t, adapterStartOptions{
		backend:              "sqlite",
		socketPath:           uniqueSocketPath(t, "sqlite-a"),
		noRegister:           true,
		useDefaultSQLitePath: true,
	})
	defer adapterA.Stop()

	adapterB := env.startAdapter(t, adapterStartOptions{
		backend:              "sqlite",
		socketPath:           uniqueSocketPath(t, "sqlite-b"),
		noRegister:           true,
		useDefaultSQLitePath: true,
	})
	defer adapterB.Stop()

	if adapterA.dbPath == adapterB.dbPath {
		t.Fatalf("expected distinct default sqlite paths, got %q", adapterA.dbPath)
	}

	setResp := callAdapterSocketRPC(t, adapterA.socketPath, "kv.set", map[string]any{
		"key":   "isolated",
		"value": "from-a",
	})
	if setResp.Error != nil {
		t.Fatalf("direct socket kv.set failed: %+v", setResp.Error)
	}

	getResp := callAdapterSocketRPC(t, adapterB.socketPath, "kv.get", map[string]any{"key": "isolated"})
	if getResp.Error == nil {
		t.Fatalf("expected adapter B to not see adapter A data, got %#v", getResp.Result)
	}
	if !strings.Contains(getResp.Error.Message, "key not found: isolated") {
		t.Fatalf("unexpected isolation error: %+v", getResp.Error)
	}
}

func testSQLiteDefaultPathStable(t *testing.T) {
	pathA1 := defaultSQLiteDBPath("/tmp/adapter-a.sock")
	pathA2 := defaultSQLiteDBPath("/tmp/adapter-a.sock")
	pathB := defaultSQLiteDBPath("/tmp/adapter-b.sock")

	if pathA1 != pathA2 {
		t.Fatalf("expected same socket to map to stable sqlite path, got %q vs %q", pathA1, pathA2)
	}
	if pathA1 == pathB {
		t.Fatalf("expected different sockets to map to different sqlite paths, got %q", pathA1)
	}
}

func testDispatchStreamingUnsupported(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "watch")
	server := startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		first := mustJSON(appRPCResponse{
			ID:     req.ID,
			Result: map[string]any{"tick": 1},
		})
		second := mustJSON(appRPCResponse{
			ID:     req.ID,
			Result: map[string]any{"tick": 2},
		})
		_, _ = conn.Write(append(first, '\n'))
		time.Sleep(50 * time.Millisecond)
		_, _ = conn.Write(append(second, '\n'))
	})
	defer server.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "kv-watch-test",
		Name:         "kv.watch",
		Description:  "Watch key changes.",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"tick":{"type":"integer"}}}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})

	stream := env.callStreamRPC(t, env.sandboxURL+"/rpc/stream", "kv.watch", map[string]any{})
	if !strings.Contains(stream, "event: started") {
		t.Fatalf("expected started SSE frame, got %s", stream)
	}
	if !strings.Contains(stream, "event: completed") {
		t.Fatalf("expected completed SSE frame, got %s", stream)
	}
	if strings.Contains(stream, "\"tick\":2") {
		t.Fatalf("expected adapter second response line to be ignored, got %s", stream)
	}
}

func testCVRCheckpointEnforcement(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("kv set", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.batch_set",
		Params: map[string]any{
			"entries": []map[string]any{{
				"key":   "cvr-key",
				"value": "cvr-value",
			}},
		},
	}})
	if err := engine.RunTask(context.Background(), task); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if task.Steps[0].CheckpointID == "" {
		t.Fatal("expected kv.batch_set to trigger a checkpoint in the orchestrator lane")
	}
	beforeDirect := env.callSandboxRPC(t, "state.list", map[string]any{})
	if beforeDirect.Error != nil {
		t.Fatalf("state.list before direct call failed: %+v", beforeDirect.Error)
	}
	beforeCount := len(mustEnvelopeData(t, beforeDirect.Result)["checkpoints"].([]any))

	direct := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "rpc-key", "value": "rpc-value"})
	if direct.Error != nil {
		t.Fatalf("direct rpc kv.set failed: %+v", direct.Error)
	}
	checkpoints := env.callSandboxRPC(t, "state.list", map[string]any{})
	if checkpoints.Error != nil {
		t.Fatalf("state.list failed: %+v", checkpoints.Error)
	}
	payload := mustEnvelopeData(t, checkpoints.Result)
	if len(payload["checkpoints"].([]any)) != beforeCount {
		t.Fatalf("expected direct /rpc call to avoid extra checkpoints, before=%d after=%d payload=%#v", beforeCount, len(payload["checkpoints"].([]any)), payload)
	}
}

func testCVRIrreversibleDecision(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	manifest := env.mustGetManifest(t, "kv.delete")
	router := sandbox.NewRouter(env.newSystemRegistry())
	router.RegisterAppRegistry(env.appRegistry)
	executor := newCoordinatorExecutor(router)
	store := &manifestStore{}
	coordinator := cvr.NewCVRCoordinator(store, fixedStrategy{
		result: cvr.StrategyResult{
			Outcome:     cvr.VerifyOutcomeFailed,
			Message:     "forced verify failure",
			RecoverHint: cvr.RecoverHintRollback,
		},
	}, cvr.NewDefaultDecisionTree())

	result, err := coordinator.Execute(context.Background(), cvr.CVRRequest{
		PrimitiveID: manifest.Name,
		SandboxID:   testSandboxID,
		Intent:      manifest.Intent,
		Params:      mustJSON(map[string]any{"key": "missing"}),
		Exec:        executor,
		Attempt:     0,
	})
	if err != nil {
		t.Fatalf("coordinator execute failed: %v", err)
	}
	if result.AppliedAction != cvr.RecoveryActionRollback {
		t.Fatalf("expected rollback for irreversible high-risk mutation, got %s", result.AppliedAction)
	}
	if result.CheckpointID == "" {
		t.Fatal("expected irreversible mutation to create a checkpoint")
	}
}

func testCVRLegacyVerifyEndpointTriggersVerification(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting")
	counting := startCountingSocketServer(t, socketPath)
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:            "counting-kv",
		Name:             "kv.set",
		Description:      "counting kv.set",
		InputSchema:      json.RawMessage(`{"type":"object"}`),
		OutputSchema:     json.RawMessage(`{"type":"object"}`),
		SocketPath:       socketPath,
		VerifyEndpoint:   "kv.verify",
		RollbackEndpoint: "state.restore",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify",
		Description:  "counting kv.verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("counting set", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.set",
		Params: map[string]any{
			"key":   "verify",
			"value": "unused",
		},
	}})
	if err := engine.RunTask(context.Background(), task); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if counting.CallCount("kv.verify") == 0 {
		t.Fatalf("expected legacy verify_endpoint to trigger kv.verify, got kv.verify count=%d", counting.CallCount("kv.verify"))
	}
	if counting.CallCount("kv.set") == 0 {
		t.Fatal("expected kv.set to be called at least once")
	}
}

func testCVRPrimitiveVerifyStrategy(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-primitive-verify")
	counting := startCountingSocketServer(t, socketPath)
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_with_verify",
		Description:  "counting kv.write_with_verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "kv.verify",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify",
		Description:  "counting kv.verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("counting set with primitive verify", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_with_verify",
		Params: map[string]any{
			"key":   "verify",
			"value": "primitive",
		},
	}})
	if err := engine.RunTask(context.Background(), task); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if counting.CallCount("kv.verify") == 0 {
		t.Fatalf("expected primitive verify to run, got kv.verify count=%d", counting.CallCount("kv.verify"))
	}
}

func testCVRCommandVerifyStrategy(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-command-verify")
	counting := startCountingSocketServer(t, socketPath)
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_with_command_verify",
		Description:  "counting kv.write_with_command_verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy: "command",
			Command:  "true",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("counting set with command verify", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_with_command_verify",
		Params: map[string]any{
			"key":   "verify",
			"value": "command",
		},
	}})
	if err := engine.RunTask(context.Background(), task); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if counting.CallCount("kv.write_with_command_verify") == 0 {
		t.Fatal("expected primary app primitive to run")
	}
}

func testCVRVerifyStrategyNone(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-none-verify")
	counting := startCountingSocketServer(t, socketPath)
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_without_verify",
		Description:  "counting kv.write_without_verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy: "none",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify",
		Description:  "counting kv.verify",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("counting set without verify", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_without_verify",
		Params: map[string]any{
			"key":   "verify",
			"value": "none",
		},
	}})
	if err := engine.RunTask(context.Background(), task); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if counting.CallCount("kv.verify") != 0 {
		t.Fatalf("expected verify.strategy=none to skip automatic verify, got kv.verify count=%d", counting.CallCount("kv.verify"))
	}
}

func testCVRVerifyFailureAffectsOutcome(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-verify-fail")
	counting := startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		switch req.Method {
		case "kv.verify_fail":
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"passed": false, "summary": "verify failed"},
			})
		default:
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"ok": true, "method": req.Method},
			})
		}
	})
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_verify_fails",
		Description:  "counting kv.write_verify_fails",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "kv.verify_fail",
		},
		Rollback: &primitive.AppPrimitiveRollback{
			Strategy:  "primitive",
			Primitive: "kv.rollback_verify_fails",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify_fail",
		Description:  "counting kv.verify_fail",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.rollback_verify_fails",
		Description:  "counting kv.rollback_verify_fails",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentRollback,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})

	executor := env.newRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("counting set with failing verify", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_verify_fails",
		Params: map[string]any{
			"key":   "verify",
			"value": "fail",
		},
	}})
	err := engine.RunTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected verify failure to fail the task")
	}
	if !strings.Contains(err.Error(), "verify failed") {
		t.Fatalf("expected verify failure in task error, got %v", err)
	}
	if counting.CallCount("kv.verify_fail") == 0 {
		t.Fatal("expected failing verify primitive to be called")
	}
	if task.Status != orchestrator.TaskPaused {
		t.Fatalf("expected task to pause on verify failure, got %s", task.Status)
	}
	if len(task.Steps) == 0 || task.Steps[0].Status != orchestrator.StepRolledBack {
		t.Fatalf("expected step to roll back after verify failure, got %+v", task.Steps)
	}
}

func testCVRLegacyRollbackEndpointTriggersRollback(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-legacy-rollback")
	counting := startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		switch req.Method {
		case "kv.verify_fail":
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"passed": false, "summary": "verify failed"},
			})
		default:
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"ok": true, "method": req.Method},
			})
		}
	})
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_with_legacy_rollback",
		Description:  "counting kv.write_with_legacy_rollback",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "kv.verify_fail",
		},
		RollbackEndpoint: "kv.rollback_legacy",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify_fail",
		Description:  "counting kv.verify_fail",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.rollback_legacy",
		Description:  "counting kv.rollback_legacy",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentRollback,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		},
	})

	executor := env.newRecordingRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("legacy rollback", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_with_legacy_rollback",
		Params:    map[string]any{"key": "verify", "value": "legacy"},
	}})
	err := engine.RunTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected rollback-triggering failure")
	}
	if counting.CallCount("kv.rollback_legacy") == 0 {
		t.Fatalf("expected legacy rollback_endpoint to trigger rollback primitive, got counts=%v", counting.counts)
	}
	if executor.CallCount("state.restore") != 0 {
		t.Fatalf("expected legacy app rollback to avoid workspace restore without checkpoint, got state.restore count=%d", executor.CallCount("state.restore"))
	}
}

func testCVRDeclaredRollbackPreferredOverStateRestore(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	seed := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "rolled", "value": "before"})
	if seed.Error != nil {
		t.Fatalf("seed kv.set failed: %+v", seed.Error)
	}

	executor := env.newRecordingRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("declared rollback", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.set",
		Params: map[string]any{
			"key":   "rolled",
			"value": "after",
			"verify_control": map[string]any{
				"force_fail": true,
				"message":    "verify failed after write",
			},
		},
	}})
	err := engine.RunTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected verify-triggered rollback failure")
	}
	if executor.CallCount("kv.rollback_set") == 0 {
		t.Fatalf("expected declared app rollback to run, got calls=%v", executor.calls)
	}
	if executor.CallCount("state.restore") != 0 {
		t.Fatalf("expected declared app rollback to be used instead of bare state.restore, got count=%d", executor.CallCount("state.restore"))
	}
}

func testCVRIrreversibleAppMutationWithoutRollbackFailsClosed(t *testing.T) {
	env := newValidationEnv(t)

	socketPath := uniqueSocketPath(t, "counting-fail-closed")
	counting := startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		switch req.Method {
		case "kv.verify_fail":
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"passed": false, "summary": "verify failed"},
			})
		default:
			_ = writeAppResponse(conn, appRPCResponse{
				ID:     req.ID,
				Result: map[string]any{"ok": true, "method": req.Method},
			})
		}
	})
	defer counting.Close()

	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.write_without_rollback",
		Description:  "counting kv.write_without_rollback",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Verify: &primitive.AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: "kv.verify_fail",
		},
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentMutation,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		},
	})
	registerManifest(t, env.sandboxURL, primitive.AppPrimitiveManifest{
		AppID:        "counting-kv",
		Name:         "kv.verify_fail",
		Description:  "counting kv.verify_fail",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	})

	executor := env.newRecordingRouterExecutor()
	engine := orchestrator.NewEngineWithStores(executor, nil, &manifestStore{})
	engine.SetAppRegistry(env.appRegistry)
	task := engine.CreateTask("fail closed without rollback", testSandboxID, []orchestrator.StepDef{{
		Primitive: "kv.write_without_rollback",
		Params:    map[string]any{"key": "verify", "value": "fail"},
	}})
	err := engine.RunTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected fail-closed error")
	}
	if !strings.Contains(err.Error(), "state.restore alone does not recover app state") {
		t.Fatalf("expected fail-closed explanation, got %v", err)
	}
	if executor.CallCount("state.restore") != 0 {
		t.Fatalf("expected fail-closed path to avoid state.restore, got count=%d", executor.CallCount("state.restore"))
	}
	if counting.CallCount("kv.verify_fail") == 0 {
		t.Fatal("expected verify primitive to run before fail-closed recovery decision")
	}
}

func testCVRRestoreDoesNotRollbackAdapterState(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	cpResp := env.callSandboxRPC(t, "state.checkpoint", map[string]any{"label": "before-adapter-state"})
	if cpResp.Error != nil {
		t.Fatalf("state.checkpoint failed: %+v", cpResp.Error)
	}
	checkpointID := mustEnvelopeData(t, cpResp.Result)["checkpoint_id"].(string)

	setResp := env.callSandboxRPC(t, "kv.set", map[string]any{"key": "rolled", "value": "forward"})
	if setResp.Error != nil {
		t.Fatalf("kv.set failed: %+v", setResp.Error)
	}

	restoreResp := env.callSandboxRPC(t, "state.restore", map[string]any{"checkpoint_id": checkpointID})
	if restoreResp.Error != nil {
		t.Fatalf("state.restore failed: %+v", restoreResp.Error)
	}

	getResp := env.callSandboxRPC(t, "kv.get", map[string]any{"key": "rolled"})
	if getResp.Error != nil {
		t.Fatalf("expected adapter state to survive filesystem restore, got %+v", getResp.Error)
	}
	if mustMap(t, getResp.Result)["value"] != "forward" {
		t.Fatalf("expected adapter state to remain after restore, got %#v", getResp.Result)
	}
}

func testCVRMacroSafeEditNotGeneric(t *testing.T) {
	env := newValidationEnv(t)
	system := env.mustListSystemPrimitives(t, env.sandboxURL)
	if !containsSystemPrimitive(system, "macro.safe_edit") {
		t.Fatalf("expected macro.safe_edit to exist, got %+v", system)
	}
	resp := env.callSandboxRPC(t, "macro.safe_call", map[string]any{})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown primitive") {
		t.Fatalf("expected macro.safe_call to be absent, got %+v", resp.Error)
	}
}

func testCVRRecoveryPolicy(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	manifest := env.mustGetManifest(t, "kv.set")
	router := sandbox.NewRouter(env.newSystemRegistry())
	router.RegisterAppRegistry(env.appRegistry)
	executor := newCoordinatorExecutor(router)

	coordinator := cvr.NewCVRCoordinator(&manifestStore{}, fixedStrategy{
		result: cvr.StrategyResult{
			Outcome:     cvr.VerifyOutcomeError,
			Message:     "forced strategy error",
			RecoverHint: cvr.RecoverHintRetry,
		},
	}, cvr.NewDefaultDecisionTree())

	result, err := coordinator.Execute(context.Background(), cvr.CVRRequest{
		PrimitiveID: manifest.Name,
		SandboxID:   testSandboxID,
		Intent:      manifest.Intent,
		Params:      mustJSON(map[string]any{"key": "recovery", "value": "path"}),
		Exec:        executor,
		Attempt:     0,
	})
	if err != nil {
		t.Fatalf("coordinator execute failed: %v", err)
	}
	if result.AppliedAction != cvr.RecoveryActionRetry {
		t.Fatalf("expected retry for non-terminal strategy error, got %s", result.AppliedAction)
	}
}

func testCVREventEmission(t *testing.T) {
	env := newValidationEnv(t)
	adapter := env.startAdapter(t, adapterStartOptions{})
	defer adapter.Stop()

	stream := env.callStreamRPC(t, env.sandboxURL+"/rpc/stream", "kv.set", map[string]any{
		"key":   "streamed",
		"value": "ok",
	})
	if !strings.Contains(stream, "event: started") || !strings.Contains(stream, "event: completed") {
		t.Fatalf("expected started/completed SSE frames, got %s", stream)
	}
	if strings.Contains(stream, "primitive.start") || strings.Contains(stream, "cvr.checkpoint") {
		t.Fatalf("did not expect primitive.start or cvr.checkpoint frames, got %s", stream)
	}

	events := env.sandboxEvents.ListAll()
	if !containsEventType(events, "rpc.started") || !containsEventType(events, "rpc.completed") {
		t.Fatalf("expected rpc lifecycle events, got %+v", events)
	}
	if containsEventType(events, "prim.started") || containsEventType(events, "cvr.checkpoint") {
		t.Fatalf("did not expect prim.* or cvr.* events, got %+v", events)
	}
}

func newValidationEnv(t *testing.T) *validationEnv {
	t.Helper()

	workspace := t.TempDir()
	registry := primitive.NewRegistry()
	registry.RegisterDefaults(workspace, primitive.DefaultOptions())

	appRegistry := primitive.NewInMemoryAppRegistry()
	sandboxEvents := &memoryEventStore{}
	sandboxServer := rpc.NewServer(registry, nil, nil)
	sandboxServer.RegisterAppRegistry(appRegistry)
	sandboxServer.AttachEventing(eventing.NewBus(sandboxEvents), sandboxEvents)
	sandboxHTTP := newTestHTTPServer(t, sandboxServer.Handler())
	t.Cleanup(sandboxHTTP.Close)

	manager := sandbox.NewManagerWithOptions(passthroughRuntimeDriver{}, sandbox.ManagerOptions{Store: sandbox.NewMemoryStore()})
	if err := manager.CreatePlaceholder(&sandbox.Sandbox{
		ID:           testSandboxID,
		Driver:       "proxy",
		Status:       sandbox.StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  sandboxHTTP.URL,
		RPCPort:      18080,
		Config:       sandbox.SandboxConfig{Driver: "proxy"},
	}); err != nil {
		t.Fatalf("seed sandbox placeholder: %v", err)
	}

	hostEvents := &memoryEventStore{}
	hostServer := rpc.NewServer(primitive.NewRegistry(), nil, manager)
	hostServer.AttachEventing(eventing.NewBus(hostEvents), hostEvents)
	hostHTTP := newTestHTTPServer(t, hostServer.Handler())
	t.Cleanup(hostHTTP.Close)

	return &validationEnv{
		t:             t,
		workspace:     workspace,
		appRegistry:   appRegistry,
		sandboxServer: sandboxServer,
		sandboxURL:    sandboxHTTP.URL,
		sandboxEvents: sandboxEvents,
		hostServer:    hostServer,
		hostURL:       hostHTTP.URL,
		hostEvents:    hostEvents,
		manager:       manager,
	}
}

type adapterStartOptions struct {
	manifest             string
	socketPath           string
	appID                string
	namespace            string
	backend              string
	dbPath               string
	noRegister           bool
	useDefaultSQLitePath bool
}

func (env *validationEnv) startAdapter(t *testing.T, opts adapterStartOptions) *adapterProcess {
	t.Helper()
	binary := buildAdapterBinary(t)
	socketPath := opts.socketPath
	if socketPath == "" {
		socketPath = uniqueSocketPath(t, "adapter")
	}
	manifestPath := opts.manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(repoRoot(t), "examples", "kv_adapter", "manifest.json")
	}
	args := []string{
		"--socket", socketPath,
		"--manifest", manifestPath,
		"--app-id", defaultString(opts.appID, defaultAppID),
		"--namespace", defaultString(opts.namespace, defaultNamespace),
		"--backend", defaultString(opts.backend, "memory"),
	}
	dbPath := opts.dbPath
	if defaultString(opts.backend, "memory") == "sqlite" {
		if dbPath == "" && !opts.useDefaultSQLitePath {
			dbPath = filepath.Join(t.TempDir(), "kv.sqlite")
		}
		if dbPath != "" {
			args = append(args, "--db-path", dbPath)
		} else {
			dbPath = defaultSQLiteDBPath(socketPath)
		}
	}
	if opts.noRegister {
		args = append(args, "--no-register")
	} else {
		args = append(args, "--rpc-endpoint", env.sandboxURL)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = repoRoot(t)
	proc := &adapterProcess{
		t:          t,
		cmd:        cmd,
		socketPath: socketPath,
		dbPath:     dbPath,
	}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start adapter: %v", err)
	}

	if opts.noRegister {
		waitFor(t, 5*time.Second, func() bool {
			_, err := os.Stat(socketPath)
			return err == nil
		})
	} else {
		waitFor(t, 5*time.Second, func() bool {
			if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				return false
			}
			items, err := env.listAppPrimitives(env.sandboxURL + "/app-primitives")
			if err != nil {
				return false
			}
			return len(items) >= 9
		})
	}

	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return proc
}

func (env *validationEnv) startAdapterExpectFailure(t *testing.T, opts adapterStartOptions) string {
	t.Helper()
	binary := buildAdapterBinary(t)
	socketPath := opts.socketPath
	if socketPath == "" {
		socketPath = uniqueSocketPath(t, "adapter-fail")
	}
	manifestPath := opts.manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(repoRoot(t), "examples", "kv_adapter", "manifest.json")
	}
	args := []string{
		"--socket", socketPath,
		"--manifest", manifestPath,
		"--app-id", defaultString(opts.appID, defaultAppID),
		"--namespace", defaultString(opts.namespace, defaultNamespace),
		"--backend", defaultString(opts.backend, "memory"),
	}
	dbPath := opts.dbPath
	if defaultString(opts.backend, "memory") == "sqlite" {
		if dbPath == "" && !opts.useDefaultSQLitePath {
			dbPath = filepath.Join(t.TempDir(), "kv.sqlite")
		}
		if dbPath != "" {
			args = append(args, "--db-path", dbPath)
		}
	}
	args = append(args, "--rpc-endpoint", env.sandboxURL)
	cmd := exec.Command(binary, args...)
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected adapter to fail but it succeeded: %s", string(output))
	}
	return string(output)
}

func (p *adapterProcess) Stop() {
	p.t.Helper()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_, _ = p.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
}

func (p *adapterProcess) Kill() {
	p.t.Helper()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return
	}
	_ = p.cmd.Process.Kill()
	_, _ = p.cmd.Process.Wait()
}

func buildAdapterBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		outDir, err := os.MkdirTemp("", "pb-kv-build-*")
		if err != nil {
			buildAdapterError = err
			return
		}
		builtAdapterPath = filepath.Join(outDir, "kv-adapter")
		cmd := exec.Command("go", "build", "-o", builtAdapterPath, "./examples/kv_adapter")
		cmd.Dir = repoRoot(t)
		output, err := cmd.CombinedOutput()
		if err != nil {
			buildAdapterError = fmt.Errorf("go build failed: %w: %s", err, string(output))
		}
	})
	if buildAdapterError != nil {
		t.Fatal(buildAdapterError)
	}
	return builtAdapterPath
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func uniqueSocketPath(t *testing.T, prefix string) string {
	t.Helper()
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.sock", prefix, time.Now().UnixNano()))
}

func (env *validationEnv) newSystemRegistry() *primitive.Registry {
	reg := primitive.NewRegistry()
	reg.RegisterDefaults(env.workspace, primitive.DefaultOptions())
	return reg
}

func (env *validationEnv) newRouterExecutor() *routerExecutor {
	registry := env.newSystemRegistry()
	router := sandbox.NewRouter(registry)
	router.RegisterAppRegistry(env.appRegistry)
	return &routerExecutor{
		router:      router,
		appRegistry: env.appRegistry,
		systemNames: registry.List(),
	}
}

func (env *validationEnv) newRecordingRouterExecutor() *recordingExecutor {
	return &recordingExecutor{inner: env.newRouterExecutor()}
}

func (r *routerExecutor) Execute(ctx context.Context, method string, params json.RawMessage) (*orchestrator.StepResult, error) {
	result, err := r.router.Route(ctx, method, params)
	if err != nil {
		return &orchestrator.StepResult{
			Success: false,
			Error: &orchestrator.StepError{
				Kind:    orchestrator.FailureUnknown,
				Code:    "EXECUTION_ERROR",
				Message: err.Error(),
				Summary: err.Error(),
			},
		}, err
	}
	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, err
	}
	return &orchestrator.StepResult{
		Success:  true,
		Data:     data,
		Duration: result.Duration,
	}, nil
}

func (r *routerExecutor) ListPrimitives() []string {
	items, _ := r.appRegistry.List(context.Background())
	out := append([]string(nil), r.systemNames...)
	for _, item := range items {
		out = append(out, item.Name)
	}
	sort.Strings(out)
	return out
}

type coordinatorExecutor struct {
	router *sandbox.Router
}

func newCoordinatorExecutor(router *sandbox.Router) *coordinatorExecutor {
	return &coordinatorExecutor{router: router}
}

func (e *coordinatorExecutor) Execute(ctx context.Context, method string, params any) (cvr.ExecuteResult, error) {
	raw, ok := params.(json.RawMessage)
	if !ok {
		raw = mustJSON(params)
	}
	result, err := e.router.Route(ctx, method, raw)
	out := cvr.ExecuteResult{Success: err == nil}
	if err != nil {
		out.ErrMsg = err.Error()
		return out, err
	}
	data, _ := json.Marshal(result.Data)
	if len(data) > 0 {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(data, &payload); err == nil {
			out.Data = payload
		}
	}
	return out, nil
}

func (m *memoryEventStore) Append(ctx context.Context, evt eventing.Event) (eventing.Event, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	evt.ID = int64(len(m.events) + 1)
	m.events = append(m.events, evt)
	return evt, nil
}

func (m *memoryEventStore) ListEvents(ctx context.Context, filter eventing.ListFilter) ([]eventing.Event, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]eventing.Event, 0, len(m.events))
	for _, evt := range m.events {
		if filter.Type != "" && evt.Type != filter.Type {
			continue
		}
		if filter.SandboxID != "" && evt.SandboxID != filter.SandboxID {
			continue
		}
		if filter.Method != "" && evt.Method != filter.Method {
			continue
		}
		out = append(out, evt)
	}
	return out, nil
}

func (m *memoryEventStore) ListAll() []eventing.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]eventing.Event(nil), m.events...)
}

func (m *manifestStore) SaveManifest(ctx context.Context, manifest cvr.CheckpointManifest) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.manifests == nil {
		m.manifests = make(map[string]cvr.CheckpointManifest)
	}
	m.manifests[manifest.CheckpointID] = manifest
	return nil
}

func (m *manifestStore) GetManifest(ctx context.Context, checkpointID string) (*cvr.CheckpointManifest, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	manifest, ok := m.manifests[checkpointID]
	if !ok {
		return nil, nil
	}
	return &manifest, nil
}

func (m *manifestStore) GetManifestChain(ctx context.Context, checkpointID string, maxDepth int) ([]cvr.CheckpointManifest, error) {
	_ = ctx
	_ = maxDepth
	manifest, err := m.GetManifest(context.Background(), checkpointID)
	if err != nil || manifest == nil {
		return nil, err
	}
	return []cvr.CheckpointManifest{*manifest}, nil
}

func (m *manifestStore) MarkCorrupted(ctx context.Context, checkpointID string, reason string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	manifest, ok := m.manifests[checkpointID]
	if !ok {
		return nil
	}
	manifest.Corrupted = true
	manifest.CorruptReason = reason
	m.manifests[checkpointID] = manifest
	return nil
}

func (passthroughRuntimeDriver) Create(ctx context.Context, config sandbox.SandboxConfig) (*sandbox.Sandbox, error) {
	_ = ctx
	_ = config
	return nil, errors.New("not implemented")
}
func (passthroughRuntimeDriver) Start(ctx context.Context, sandboxID string) error {
	_ = ctx
	_ = sandboxID
	return nil
}
func (passthroughRuntimeDriver) Stop(ctx context.Context, sandboxID string) error {
	_ = ctx
	_ = sandboxID
	return nil
}
func (passthroughRuntimeDriver) Destroy(ctx context.Context, sandboxID string) error {
	_ = ctx
	_ = sandboxID
	return nil
}
func (passthroughRuntimeDriver) Exec(ctx context.Context, sandboxID string, cmd sandbox.ExecCommand) (*sandbox.ExecResult, error) {
	_ = ctx
	_ = sandboxID
	_ = cmd
	return &sandbox.ExecResult{}, nil
}
func (passthroughRuntimeDriver) Inspect(ctx context.Context, sandboxID string) (*sandbox.Sandbox, error) {
	_ = ctx
	return &sandbox.Sandbox{ID: sandboxID, Status: sandbox.StatusRunning}, nil
}
func (passthroughRuntimeDriver) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	_ = ctx
	_ = sandboxID
	return sandbox.StatusRunning, nil
}
func (passthroughRuntimeDriver) Capabilities() []sandbox.RuntimeCapability { return nil }
func (passthroughRuntimeDriver) Name() string                              { return "proxy" }

func (env *validationEnv) callSandboxRPC(t *testing.T, method string, params any) httpRPCResponse {
	t.Helper()
	return env.callRPC(t, env.sandboxURL+"/rpc", method, params, nil)
}

func (env *validationEnv) callHostSandboxRPC(t *testing.T, method string, params any) httpRPCResponse {
	t.Helper()
	return env.callRPC(t, env.hostURL+"/sandboxes/"+testSandboxID+"/rpc", method, params, nil)
}

func (env *validationEnv) callRPC(t *testing.T, endpoint, method string, params any, headers map[string]string) httpRPCResponse {
	t.Helper()
	body := mustJSON(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustJSON(params),
		ID:      method + "-req",
	})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create rpc request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do rpc request: %v", err)
	}
	defer resp.Body.Close()
	var rpcResp httpRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	return rpcResp
}

func (env *validationEnv) callStreamRPC(t *testing.T, endpoint, method string, params any) string {
	t.Helper()
	body := mustJSON(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustJSON(params),
		ID:      method + "-stream",
	})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do stream request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream body: %v", err)
	}
	return string(raw)
}

func callAdapterSocketRPC(t *testing.T, socketPath, method string, params any) appRPCResponse {
	t.Helper()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial adapter socket %s: %v", socketPath, err)
	}
	defer conn.Close()

	req := appRPCRequest{
		ID:     "socket-test",
		Method: method,
		Params: mustJSON(params),
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode adapter socket request: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read adapter socket response: %v", err)
	}
	var resp appRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode adapter socket response: %v", err)
	}
	return resp
}

func (env *validationEnv) listAppPrimitives(url string) ([]primitive.AppPrimitiveManifest, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		AppPrimitives []primitive.AppPrimitiveManifest `json:"app_primitives"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.AppPrimitives, nil
}

func (env *validationEnv) mustListSandboxAppPrimitives(t *testing.T) []primitive.AppPrimitiveManifest {
	t.Helper()
	items, err := env.listAppPrimitives(env.sandboxURL + "/app-primitives")
	if err != nil {
		t.Fatalf("list sandbox app primitives: %v", err)
	}
	return items
}

func (env *validationEnv) mustListHostSandboxAppPrimitives(t *testing.T) []primitive.AppPrimitiveManifest {
	t.Helper()
	items, err := env.listAppPrimitives(env.hostURL + "/api/v1/sandboxes/" + testSandboxID + "/app-primitives")
	if err != nil {
		t.Fatalf("list host sandbox app primitives: %v", err)
	}
	return items
}

func (env *validationEnv) mustListSystemPrimitives(t *testing.T, baseURL string) []map[string]any {
	t.Helper()
	resp, err := http.Get(baseURL + "/primitives")
	if err != nil {
		t.Fatalf("get /primitives: %v", err)
	}
	defer resp.Body.Close()
	var payload struct {
		Primitives []map[string]any `json:"primitives"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode /primitives: %v", err)
	}
	return payload.Primitives
}

func (env *validationEnv) loadManifestTemplate(t *testing.T) []primitive.AppPrimitiveManifest {
	t.Helper()
	path := filepath.Join(repoRoot(t), "examples", "kv_adapter", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest template: %v", err)
	}
	var manifests []primitive.AppPrimitiveManifest
	if err := json.Unmarshal(data, &manifests); err != nil {
		t.Fatalf("decode manifest template: %v", err)
	}
	return manifests
}

func writeManifestFile(t *testing.T, dir string, manifests []primitive.AppPrimitiveManifest) string {
	t.Helper()
	path := filepath.Join(dir, "manifest.json")
	data, err := json.MarshalIndent(manifests, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest file: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest file: %v", err)
	}
	return path
}

func registerManifest(t *testing.T, sandboxURL string, manifest primitive.AppPrimitiveManifest) {
	t.Helper()
	reqBody := mustJSON(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  "app.register",
		Params:  mustJSON(manifest),
		ID:      "register-" + manifest.Name,
	})
	req, err := http.NewRequest(http.MethodPost, sandboxURL+"/rpc", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("new register request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register manifest request failed: %v", err)
	}
	defer resp.Body.Close()
	var rpcResp httpRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode register manifest response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("register manifest failed: %+v", rpcResp.Error)
	}
}

func registerManifestExpectError(t *testing.T, sandboxURL string, manifest primitive.AppPrimitiveManifest) httpRPCResponse {
	t.Helper()
	reqBody := mustJSON(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  "app.register",
		Params:  mustJSON(manifest),
		ID:      "register-" + manifest.Name,
	})
	req, err := http.NewRequest(http.MethodPost, sandboxURL+"/rpc", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("new register request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register manifest request failed: %v", err)
	}
	defer resp.Body.Close()
	var rpcResp httpRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode register manifest response: %v", err)
	}
	return rpcResp
}

func (env *validationEnv) mustGetManifest(t *testing.T, name string) primitive.AppPrimitiveManifest {
	t.Helper()
	items := env.mustListSandboxAppPrimitives(t)
	manifest, ok := manifestByName(items)[name]
	if !ok {
		t.Fatalf("manifest %s not found", name)
	}
	return manifest
}

func containsManifest(items []primitive.AppPrimitiveManifest, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func manifestByName(items []primitive.AppPrimitiveManifest) map[string]primitive.AppPrimitiveManifest {
	out := make(map[string]primitive.AppPrimitiveManifest, len(items))
	for _, item := range items {
		out[item.Name] = item
	}
	return out
}

func primitiveSchemaByName(items []map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(items))
	for _, item := range items {
		name, _ := item["name"].(string)
		if name == "" {
			continue
		}
		out[name] = item
	}
	return out
}

func (r *recordingExecutor) Execute(ctx context.Context, method string, params json.RawMessage) (*orchestrator.StepResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, method)
	r.mu.Unlock()
	return r.inner.Execute(ctx, method, params)
}

func (r *recordingExecutor) ListPrimitives() []string {
	return r.inner.ListPrimitives()
}

func (r *recordingExecutor) CallCount(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if call == method {
			count++
		}
	}
	return count
}

func containsSystemPrimitive(items []map[string]any, name string) bool {
	for _, item := range items {
		if item["name"] == name {
			return true
		}
	}
	return false
}

func mustMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode map value: %v", err)
	}
	return out
}

func mustEnvelopeData(t *testing.T, value any) map[string]any {
	t.Helper()
	payload := mustMap(t, value)
	data, ok := payload["data"]
	if !ok {
		t.Fatalf("expected result envelope with data, got %#v", payload)
	}
	return mustMap(t, data)
}

func containsEventType(events []eventing.Event, typ string) bool {
	for _, evt := range events {
		if evt.Type == typ {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

type customSocketServer struct {
	listener net.Listener
	mu       sync.Mutex
	counts   map[string]int
}

func startCustomSocketServer(t *testing.T, socketPath string, handler func(appRPCRequest, net.Conn)) *customSocketServer {
	t.Helper()
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		skipIfListenUnavailable(t, err)
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	server := &customSocketServer{
		listener: listener,
		counts:   make(map[string]int),
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req appRPCRequest
				if err := json.Unmarshal(line, &req); err != nil {
					return
				}
				server.mu.Lock()
				server.counts[req.Method]++
				server.mu.Unlock()
				handler(req, conn)
			}(conn)
		}
	}()
	t.Cleanup(server.Close)
	return server
}

func newTestHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	defer func() {
		if recovered := recover(); recovered != nil {
			if skipIfListenUnavailableValue(t, recovered) {
				return
			}
			panic(recovered)
		}
	}()

	return httptest.NewServer(handler)
}

func skipIfListenUnavailable(t *testing.T, err error) {
	t.Helper()
	if isListenUnavailable(err.Error()) {
		t.Skipf("skipping test: listen unavailable in current environment: %v", err)
	}
}

func skipIfListenUnavailableValue(t *testing.T, recovered any) bool {
	t.Helper()
	if recovered == nil {
		return false
	}
	if isListenUnavailable(fmt.Sprint(recovered)) {
		t.Skipf("skipping test: listen unavailable in current environment: %v", recovered)
		return true
	}
	return false
}

func isListenUnavailable(message string) bool {
	return strings.Contains(message, "bind: operation not permitted") ||
		strings.Contains(message, "failed to listen on a port")
}

func startCountingSocketServer(t *testing.T, socketPath string) *customSocketServer {
	t.Helper()
	return startCustomSocketServer(t, socketPath, func(req appRPCRequest, conn net.Conn) {
		_ = writeAppResponse(conn, appRPCResponse{
			ID: req.ID,
			Result: map[string]any{
				"ok":     true,
				"method": req.Method,
			},
		})
	})
}

func (s *customSocketServer) CallCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[method]
}

func (s *customSocketServer) Close() {
	if s == nil || s.listener == nil {
		return
	}
	_ = s.listener.Close()
}
