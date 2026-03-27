package sandbox

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

// ---------------------------------------------------------------------------
// Shared fake client helpers
// ---------------------------------------------------------------------------

// fakeKubernetesClient is a test double for KubernetesClient that records
// the last manifest applied and deletions requested, and returns a
// configurable pod status.
type fakeKubernetesClient struct {
	manifest     KubernetesManifest
	pod          *KubernetesPodStatus
	podErr       error
	deletedNames KubernetesResourceNames
	execResult   *ExecResult
	pfHandle     PortForwardHandle
	pfClosed     bool
}

func (f *fakeKubernetesClient) Apply(ctx context.Context, manifest KubernetesManifest) error {
	f.manifest = manifest
	return nil
}

func (f *fakeKubernetesClient) Delete(ctx context.Context, names KubernetesResourceNames) error {
	f.deletedNames = names
	return nil
}

func (f *fakeKubernetesClient) GetPod(ctx context.Context, namespace, name string) (*KubernetesPodStatus, error) {
	if f.podErr != nil {
		return nil, f.podErr
	}
	return f.pod, nil
}

func (f *fakeKubernetesClient) Exec(ctx context.Context, namespace, name string, cmd ExecCommand) (*ExecResult, error) {
	if f.execResult == nil {
		return &ExecResult{ExitCode: 0}, nil
	}
	return f.execResult, nil
}

func (f *fakeKubernetesClient) StartPortForward(ctx context.Context, namespace, name string, localPort, remotePort int) (PortForwardHandle, error) {
	if f.pfHandle != nil {
		return f.pfHandle, nil
	}
	return fakePortForwardHandle("http://127.0.0.1"), nil
}

// trackingPortForwardHandle records when Close is called.
type trackingPortForwardHandle struct {
	address string
	closed  bool
}

func (h *trackingPortForwardHandle) Address() string { return h.address }
func (h *trackingPortForwardHandle) Close() error    { h.closed = true; return nil }

type fakePortForwardHandle string

func (f fakePortForwardHandle) Address() string { return string(f) }
func (f fakePortForwardHandle) Close() error    { return nil }

// makeRunningPod returns a KubernetesPodStatus for a running pod.
func makeRunningPod(sandboxID, namespace string) *KubernetesPodStatus {
	return &KubernetesPodStatus{
		Name:        sandboxID,
		Namespace:   namespace,
		Phase:       "Running",
		Ready:       true,
		ContainerID: "pod/" + sandboxID,
		Labels:      map[string]string{"primitivebox.sandbox_id": sandboxID},
	}
}

// defaultLookup returns a lookup that always returns the given namespace.
func defaultLookup(namespace string) SandboxLookup {
	return func(ctx context.Context, sandboxID string) (*Sandbox, bool, error) {
		return &Sandbox{ID: sandboxID, Namespace: namespace}, true, nil
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriverBuildsManifestAndMapsStatus (original, kept intact)
// ---------------------------------------------------------------------------

func TestKubernetesDriverBuildsManifestAndMapsStatus(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{
		pod: &KubernetesPodStatus{
			Name:        "sb-k8s",
			Namespace:   "agents",
			Phase:       "Running",
			Ready:       true,
			ContainerID: "pod/sb-k8s",
			Labels:      map[string]string{"primitivebox.sandbox_id": "sb-k8s"},
		},
	}
	driver := NewKubernetesDriver(client).WithSandboxLookup(func(ctx context.Context, sandboxID string) (*Sandbox, bool, error) {
		return &Sandbox{ID: sandboxID, Namespace: "agents"}, true, nil
	})

	sb, err := driver.Create(context.Background(), SandboxConfig{
		Driver:      "kubernetes",
		Image:       "primitivebox-sandbox:latest",
		Namespace:   "agents",
		User:        "1000:1000",
		CPULimit:    2,
		MemoryLimit: 1024,
		DiskLimit:   2048,
		NetworkPolicy: NetworkPolicy{
			Mode:       NetworkModePolicy,
			AllowCIDRs: []string{"10.0.0.0/24"},
			AllowPorts: []int{443},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("skipping test: port forwarding listener unavailable in current environment: %v", err)
		}
		t.Fatalf("create kubernetes sandbox: %v", err)
	}
	if client.manifest.Resources.Namespace != "agents" {
		t.Fatalf("unexpected manifest namespace: %+v", client.manifest.Resources)
	}
	if client.manifest.Pod == nil || client.manifest.Pod.Spec.Containers[0].Image != "primitivebox-sandbox:latest" {
		t.Fatalf("expected pod manifest with image, got %+v", client.manifest.Pod)
	}
	if client.manifest.Service == nil || client.manifest.Service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("expected service manifest, got %+v", client.manifest.Service)
	}
	if client.manifest.WorkspacePVC == nil {
		t.Fatalf("expected pvc manifest")
	}
	if storage := client.manifest.WorkspacePVC.Spec.Resources.Requests[corev1.ResourceStorage]; storage.Cmp(apiresource.MustParse("2048Mi")) != 0 {
		t.Fatalf("unexpected pvc storage request: %s", storage.String())
	}
	// NetworkModePolicy: expect DNS rule + CIDR/port rule = 2 egress rules.
	if client.manifest.NetworkPolicy == nil || len(client.manifest.NetworkPolicy.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules (dns + cidr), got %+v", client.manifest.NetworkPolicy)
	}

	client.pod.Name = sb.ID
	client.pod.Namespace = "agents"

	inspected, err := driver.Inspect(context.Background(), sb.ID)
	if err != nil {
		t.Fatalf("inspect kubernetes sandbox: %v", err)
	}
	if inspected.Status != StatusRunning {
		t.Fatalf("expected running status, got %s", inspected.Status)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_PodManifestDefaults verifies restartPolicy and DNS
// ---------------------------------------------------------------------------

func TestKubernetesDriver_PodManifestDefaults(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{}
	driver := NewKubernetesDriver(client)

	_, err := driver.Create(context.Background(), SandboxConfig{
		Image: "my-image:latest",
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port forwarding unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	pod := client.manifest.Pod
	if pod == nil {
		t.Fatal("expected pod manifest")
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("expected RestartPolicy=Never, got %s", pod.Spec.RestartPolicy)
	}
	if pod.Spec.DNSPolicy != corev1.DNSClusterFirst {
		t.Errorf("expected DNSPolicy=ClusterFirst, got %s", pod.Spec.DNSPolicy)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_NetworkModeNone
// ---------------------------------------------------------------------------

func TestKubernetesDriver_NetworkModeNone(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{}
	driver := NewKubernetesDriver(client)

	_, err := driver.Create(context.Background(), SandboxConfig{
		Image: "sandbox:latest",
		NetworkPolicy: NetworkPolicy{
			Mode: NetworkModeNone,
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	np := client.manifest.NetworkPolicy
	if np == nil {
		t.Fatal("expected NetworkPolicy manifest for NetworkModeNone")
	}
	if len(np.Spec.Egress) != 0 {
		t.Errorf("NetworkModeNone: expected zero egress rules, got %d", len(np.Spec.Egress))
	}
	if len(np.Spec.PolicyTypes) == 0 || np.Spec.PolicyTypes[0] != "Egress" {
		t.Errorf("expected Egress PolicyType, got %v", np.Spec.PolicyTypes)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_NetworkModeFull
// ---------------------------------------------------------------------------

func TestKubernetesDriver_NetworkModeFull(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{}
	driver := NewKubernetesDriver(client)

	_, err := driver.Create(context.Background(), SandboxConfig{
		Image: "sandbox:latest",
		NetworkPolicy: NetworkPolicy{
			Mode: NetworkModeFull,
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	if client.manifest.NetworkPolicy != nil {
		t.Errorf("NetworkModeFull: expected no NetworkPolicy manifest, got one")
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_NetworkModePolicy_DNSEgressIncluded
// ---------------------------------------------------------------------------

func TestKubernetesDriver_NetworkModePolicy_DNSEgressIncluded(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{}
	driver := NewKubernetesDriver(client)

	_, err := driver.Create(context.Background(), SandboxConfig{
		Image: "sandbox:latest",
		NetworkPolicy: NetworkPolicy{
			Mode:       NetworkModePolicy,
			AllowCIDRs: []string{"192.168.1.0/24"},
			AllowPorts: []int{5432},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	np := client.manifest.NetworkPolicy
	if np == nil {
		t.Fatal("expected NetworkPolicy for NetworkModePolicy")
	}
	// Should have: [0] = DNS rule, [1] = CIDR+port rule
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules, got %d: %+v", len(np.Spec.Egress), np.Spec.Egress)
	}
	dnsRule := np.Spec.Egress[0]
	if len(dnsRule.Ports) != 2 {
		t.Errorf("DNS rule: expected 2 ports (UDP+TCP 53), got %d", len(dnsRule.Ports))
	}
	for _, p := range dnsRule.Ports {
		if p.Port.IntValue() != 53 {
			t.Errorf("DNS rule: unexpected port %d", p.Port.IntValue())
		}
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_Stop_ClosesPortForward
// ---------------------------------------------------------------------------

func TestKubernetesDriver_Stop_ClosesPortForward(t *testing.T) {
	t.Parallel()

	handle := &trackingPortForwardHandle{address: "http://127.0.0.1:19999"}
	client := &fakeKubernetesClient{pfHandle: handle}
	driver := NewKubernetesDriver(client).WithSandboxLookup(defaultLookup("default"))

	sb, err := driver.Create(context.Background(), SandboxConfig{
		Image: "sandbox:latest",
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	// Manually wire the track handle into the driver's portForwards map.
	driver.mu.Lock()
	driver.portForwards[sb.ID] = handle
	driver.mu.Unlock()

	if err := driver.Stop(context.Background(), sb.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !handle.closed {
		t.Error("expected port-forward handle to be closed after Stop")
	}

	// Stopping again should be a no-op.
	if err := driver.Stop(context.Background(), sb.ID); err != nil {
		t.Fatalf("second stop should not error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_Destroy_DeletesResources
// ---------------------------------------------------------------------------

func TestKubernetesDriver_Destroy_DeletesResources(t *testing.T) {
	t.Parallel()

	const namespace = "staging"
	client := &fakeKubernetesClient{}
	driver := NewKubernetesDriver(client).WithSandboxLookup(defaultLookup(namespace))

	sb, err := driver.Create(context.Background(), SandboxConfig{
		Image:     "sandbox:latest",
		Namespace: namespace,
	})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	if err := driver.Destroy(context.Background(), sb.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	if client.deletedNames.PodName != sb.ID {
		t.Errorf("expected pod_name=%s in delete call, got %q", sb.ID, client.deletedNames.PodName)
	}
	if client.deletedNames.Namespace != namespace {
		t.Errorf("expected namespace=%s in delete call, got %q", namespace, client.deletedNames.Namespace)
	}
	if client.deletedNames.WorkspacePVCName != sb.ID+"-workspace" {
		t.Errorf("unexpected pvc name: %q", client.deletedNames.WorkspacePVCName)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_Exec_DelegatesToClient
// ---------------------------------------------------------------------------

func TestKubernetesDriver_Exec_DelegatesToClient(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{
		execResult: &ExecResult{ExitCode: 0, Stdout: "hello\n"},
	}
	driver := NewKubernetesDriver(client).WithSandboxLookup(defaultLookup("default"))

	sb, err := driver.Create(context.Background(), SandboxConfig{Image: "sandbox:latest"})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	result, err := driver.Exec(context.Background(), sb.ID, ExecCommand{
		Command: "echo",
		Args:    []string{"hello"},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_UnavailableClient_GracefulErrors
// ---------------------------------------------------------------------------

func TestKubernetesDriver_UnavailableClient_GracefulErrors(t *testing.T) {
	t.Parallel()

	// A nil client simulates a cluster that is unreachable at boot time.
	driver := NewKubernetesDriver(nil).WithSandboxLookup(defaultLookup("default"))

	if _, err := driver.Create(context.Background(), SandboxConfig{Image: "x"}); err == nil {
		t.Error("Create with nil client should return error")
	}
	if err := driver.Start(context.Background(), "sb-missing"); err == nil {
		t.Error("Start with nil client should return error")
	}
	if err := driver.Destroy(context.Background(), "sb-missing"); err == nil {
		t.Error("Destroy with nil client should return error")
	}
	if _, err := driver.Exec(context.Background(), "sb-missing", ExecCommand{Command: "ls"}); err == nil {
		t.Error("Exec with nil client should return error")
	}
	if _, err := driver.Inspect(context.Background(), "sb-missing"); err == nil {
		t.Error("Inspect with nil client should return error")
	}
}

// ---------------------------------------------------------------------------
// TestKubernetesDriver_Inspect_PodNotFound_ReturnsDestroyed
// ---------------------------------------------------------------------------

func TestKubernetesDriver_Inspect_PodNotFound_ReturnsDestroyed(t *testing.T) {
	t.Parallel()

	client := &fakeKubernetesClient{
		podErr: ErrKubernetesResourceNotFound,
	}
	driver := NewKubernetesDriver(client).WithSandboxLookup(defaultLookup("default"))

	// Seed a sandbox record by creating one with the fake client.
	sb, err := driver.Create(context.Background(), SandboxConfig{Image: "sandbox:latest"})
	if err != nil {
		if strings.Contains(err.Error(), "bind: operation not permitted") {
			t.Skipf("port listener unavailable: %v", err)
		}
		t.Fatalf("create: %v", err)
	}

	inspected, err := driver.Inspect(context.Background(), sb.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if inspected.Status != StatusDestroyed {
		t.Errorf("expected StatusDestroyed when pod not found, got %s", inspected.Status)
	}
}
