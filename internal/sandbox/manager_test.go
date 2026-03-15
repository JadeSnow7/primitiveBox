package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestManagerPersistsViaStore(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	driver := &fakeRuntimeDriver{
		createSandbox: &Sandbox{
			ID:          "sb-test01",
			ContainerID: "ctr-123",
			Status:      StatusStopped,
			RPCEndpoint: "http://127.0.0.1:18080",
			RPCPort:     18080,
		},
	}

	manager := NewManagerWithOptions(driver, ManagerOptions{Store: store})
	created, err := manager.Create(context.Background(), SandboxConfig{
		Image:       "primitivebox-sandbox:latest",
		MountSource: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	another := NewManagerWithOptions(driver, ManagerOptions{Store: store})
	got, ok := another.Get(created.ID)
	if !ok {
		t.Fatalf("expected sandbox %s to reload from store", created.ID)
	}
	if got.ContainerID != "ctr-123" {
		t.Fatalf("expected container id ctr-123, got %s", got.ContainerID)
	}
}

func TestManagerListRefreshesStatus(t *testing.T) {
	t.Parallel()

	registryDir := t.TempDir()
	driver := &fakeRuntimeDriver{
		createSandbox: &Sandbox{
			ID:          "sb-test02",
			ContainerID: "ctr-234",
			Status:      StatusStopped,
			RPCEndpoint: "http://127.0.0.1:18081",
			RPCPort:     18081,
		},
		statuses: map[string]SandboxStatus{
			"sb-test02": StatusRunning,
		},
	}

	manager := NewManagerWithRegistryDir(driver, registryDir)
	if _, err := manager.Create(context.Background(), SandboxConfig{MountSource: t.TempDir()}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one sandbox, got %d", len(items))
	}
	if items[0].Status != StatusRunning {
		t.Fatalf("expected refreshed running status, got %s", items[0].Status)
	}
}

type fakeRuntimeDriver struct {
	createSandbox *Sandbox
	statuses      map[string]SandboxStatus
}

func (f *fakeRuntimeDriver) Create(ctx context.Context, config SandboxConfig) (*Sandbox, error) {
	if f.createSandbox == nil {
		return nil, errors.New("no sandbox configured")
	}
	sb := cloneSandbox(f.createSandbox)
	sb.Config = config
	return sb, nil
}

func (f *fakeRuntimeDriver) Start(ctx context.Context, sandboxID string) error   { return nil }
func (f *fakeRuntimeDriver) Stop(ctx context.Context, sandboxID string) error    { return nil }
func (f *fakeRuntimeDriver) Destroy(ctx context.Context, sandboxID string) error { return nil }

func (f *fakeRuntimeDriver) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	return &ExecResult{ExitCode: 0}, nil
}

func (f *fakeRuntimeDriver) Inspect(ctx context.Context, sandboxID string) (*Sandbox, error) {
	sb := cloneSandbox(f.createSandbox)
	if sb == nil {
		sb = &Sandbox{ID: sandboxID}
	}
	if status, ok := f.statuses[sandboxID]; ok {
		sb.Status = status
	}
	return sb, nil
}

func (f *fakeRuntimeDriver) Status(ctx context.Context, sandboxID string) (SandboxStatus, error) {
	if status, ok := f.statuses[sandboxID]; ok {
		return status, nil
	}
	return StatusStopped, nil
}

func (f *fakeRuntimeDriver) Capabilities() []RuntimeCapability { return nil }
func (f *fakeRuntimeDriver) Name() string                      { return "fake" }

func TestSandboxJSONRoundTrip(t *testing.T) {
	t.Parallel()

	sb := &Sandbox{
		ID:           "sb-json",
		ContainerID:  "ctr-json",
		Status:       StatusRunning,
		HealthStatus: "healthy",
		RPCEndpoint:  "http://127.0.0.1:19090",
		RPCPort:      19090,
	}

	data, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal sandbox: %v", err)
	}
	var roundTrip Sandbox
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal sandbox: %v", err)
	}
	if roundTrip.ContainerID != sb.ContainerID || roundTrip.RPCPort != sb.RPCPort {
		t.Fatalf("unexpected round trip sandbox: %+v", roundTrip)
	}
}

func TestManagerTouchAndReapExpired(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	driver := &fakeRuntimeDriver{
		createSandbox: &Sandbox{
			ID:     "sb-expiring",
			Status: StatusStopped,
		},
		statuses: map[string]SandboxStatus{
			"sb-expiring": StatusStopped,
		},
	}
	manager := NewManagerWithOptions(driver, ManagerOptions{Store: store})
	sb, err := manager.Create(context.Background(), SandboxConfig{
		Driver:      "fake",
		MountSource: t.TempDir(),
		Lifecycle: LifecyclePolicy{
			TTLSeconds:     1,
			IdleTTLSeconds: 1,
		},
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := manager.Touch(context.Background(), sb.ID); err != nil {
		t.Fatalf("touch sandbox: %v", err)
	}
	touched, ok := manager.Get(sb.ID)
	if !ok || touched.ExpiresAt == 0 {
		t.Fatalf("expected touched sandbox to have expires_at, got %+v", touched)
	}

	seeded := cloneSandbox(touched)
	seeded.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	if err := store.Upsert(context.Background(), seeded); err != nil {
		t.Fatalf("seed expired sandbox: %v", err)
	}

	reaped, err := manager.ReapExpired(context.Background(), 10)
	if err != nil {
		t.Fatalf("reap expired: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("expected 1 reaped sandbox, got %d", reaped)
	}
	if _, ok := manager.Get(sb.ID); ok {
		t.Fatalf("expected sandbox %s to be deleted", sb.ID)
	}
}
