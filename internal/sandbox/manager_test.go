package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestManagerPersistsRegistry(t *testing.T) {
	t.Parallel()

	registryDir := t.TempDir()
	driver := &fakeRuntimeDriver{
		createSandbox: &Sandbox{
			ID:          "sb-test01",
			ContainerID: "ctr-123",
			Status:      StatusStopped,
			RPCEndpoint: "http://127.0.0.1:18080",
			RPCPort:     18080,
		},
	}

	manager := NewManagerWithRegistryDir(driver, registryDir)
	created, err := manager.Create(context.Background(), SandboxConfig{
		Image:       "primitivebox-sandbox:latest",
		MountSource: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	reloaded := NewManagerWithRegistryDir(driver, registryDir)
	got, ok := reloaded.Get(created.ID)
	if !ok {
		t.Fatalf("expected sandbox %s to reload from registry", created.ID)
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

func (f *fakeRuntimeDriver) Status(ctx context.Context, sandboxID string) (SandboxStatus, error) {
	if status, ok := f.statuses[sandboxID]; ok {
		return status, nil
	}
	return StatusStopped, nil
}

func (f *fakeRuntimeDriver) Name() string { return "fake" }

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
