package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtrace"
	"primitivebox/internal/sandbox"
)

func TestSQLiteStorePersistsSandboxesAndEvents(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStore(t.TempDir() + "/controlplane.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	sb := &sandbox.Sandbox{
		ID:        "sb-store",
		Driver:    "docker",
		Namespace: "default",
		Config: sandbox.SandboxConfig{
			Driver:      "docker",
			MountSource: t.TempDir(),
			MountTarget: "/workspace",
		},
		Status:         sandbox.StatusRunning,
		HealthStatus:   "healthy",
		RPCEndpoint:    "http://127.0.0.1:19090",
		RPCPort:        19090,
		CreatedAt:      time.Now().Unix(),
		LastAccessedAt: time.Now().Unix(),
		ExpiresAt:      time.Now().Add(time.Minute).Unix(),
		Labels:         map[string]string{"managed-by": "primitivebox"},
	}
	if err := store.Upsert(context.Background(), sb); err != nil {
		t.Fatalf("upsert sandbox: %v", err)
	}

	got, ok, err := store.Get(context.Background(), sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if !ok {
		t.Fatalf("expected sandbox %s", sb.ID)
	}
	if got.Driver != "docker" || got.RPCPort != 19090 {
		t.Fatalf("unexpected sandbox roundtrip: %+v", got)
	}

	event, err := store.Append(context.Background(), eventing.Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "shell.output",
		SandboxID: sb.ID,
		Method:    "shell.exec",
		Stream:    "stdout",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if event.ID == 0 {
		t.Fatalf("expected auto-incremented event id")
	}

	events, err := store.ListEvents(context.Background(), eventing.ListFilter{SandboxID: sb.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Message != "hello" {
		t.Fatalf("unexpected events: %+v", events)
	}

	record := runtrace.StepRecord{
		TaskID:      "task-1",
		TraceID:     "trace-1",
		SessionID:   "session-1",
		AttemptID:   "attempt-1",
		SandboxID:   sb.ID,
		StepID:      "step-1",
		Primitive:   "repo.patch_symbol",
		DurationMs:  42,
		FailureKind: "",
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.RecordTraceStep(context.Background(), record); err != nil {
		t.Fatalf("record trace step: %v", err)
	}

	traces, err := store.ListTraceSteps(context.Background(), sb.ID, 10)
	if err != nil {
		t.Fatalf("list trace steps: %v", err)
	}
	if len(traces) != 1 || traces[0].Primitive != "repo.patch_symbol" {
		t.Fatalf("unexpected trace steps: %+v", traces)
	}

	trace, err := store.GetTraceStep(context.Background(), sb.ID, "step-1")
	if err != nil {
		t.Fatalf("get trace step: %v", err)
	}
	if trace == nil || trace.TraceID != "trace-1" {
		t.Fatalf("unexpected trace step: %+v", trace)
	}
}

func TestSQLiteStoreImportsLegacyRegistry(t *testing.T) {
	t.Parallel()

	registryDir := t.TempDir()
	store, err := OpenSQLiteStore(t.TempDir() + "/controlplane.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	legacy := &sandbox.Sandbox{
		ID:          "sb-imported",
		Driver:      "docker",
		Status:      sandbox.StatusStopped,
		CreatedAt:   time.Now().Unix(),
		Config:      sandbox.SandboxConfig{Driver: "docker", MountTarget: "/workspace"},
		RPCEndpoint: "http://127.0.0.1:18080",
		RPCPort:     18080,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy sandbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(registryDir, legacy.ID+".json"), data, 0o644); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}

	imported, err := store.ImportLegacyRegistryDir(context.Background(), registryDir)
	if err != nil {
		t.Fatalf("import legacy registry: %v", err)
	}
	if imported != 1 {
		t.Fatalf("expected 1 imported sandbox, got %d", imported)
	}
}

func TestSQLiteAppRegistry_PersistsAvailabilityAndLifecycleEvents(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStore(t.TempDir() + "/controlplane.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	bus := eventing.NewBus(store)
	registry := NewSQLiteAppRegistry(store, bus)

	manifest := primitive.AppPrimitiveManifest{
		AppID:        "kv-app",
		Name:         "kv.get",
		Description:  "Fetch a key.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		SocketPath:   "/tmp/kv.sock",
		Intent: cvr.PrimitiveIntent{
			Category:   cvr.IntentQuery,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		},
	}

	if err := registry.Register(context.Background(), manifest); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	got, err := registry.Get(context.Background(), manifest.Name)
	if err != nil {
		t.Fatalf("get adapter manifest: %v", err)
	}
	if got == nil || got.Availability != primitive.AppPrimitiveActive {
		t.Fatalf("expected active manifest after registration, got %+v", got)
	}

	if err := registry.MarkUnavailable(context.Background(), manifest.AppID); err != nil {
		t.Fatalf("mark unavailable: %v", err)
	}

	got, err = registry.Get(context.Background(), manifest.Name)
	if err != nil {
		t.Fatalf("get unavailable adapter manifest: %v", err)
	}
	if got == nil || got.Availability != primitive.AppPrimitiveUnavailable {
		t.Fatalf("expected unavailable manifest after mark unavailable, got %+v", got)
	}

	reactivated := manifest
	reactivated.SocketPath = "/tmp/kv-v2.sock"
	if err := registry.Register(context.Background(), reactivated); err != nil {
		t.Fatalf("reactivate adapter: %v", err)
	}

	got, err = registry.Get(context.Background(), manifest.Name)
	if err != nil {
		t.Fatalf("get reactivated adapter manifest: %v", err)
	}
	if got == nil || got.Availability != primitive.AppPrimitiveActive || got.SocketPath != reactivated.SocketPath {
		t.Fatalf("expected active reactivated manifest, got %+v", got)
	}

	events, err := store.ListEvents(context.Background(), eventing.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list adapter lifecycle events: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected at least 3 lifecycle events, got %+v", events)
	}
	if events[2].Type != "adapter.registered" || events[1].Type != "adapter.unavailable" || events[0].Type != "adapter.reactivated" {
		t.Fatalf("unexpected lifecycle event order: %+v", events[:3])
	}
}
