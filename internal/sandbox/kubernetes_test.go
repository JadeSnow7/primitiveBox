package sandbox

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

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
	if client.manifest.NetworkPolicy == nil || len(client.manifest.NetworkPolicy.Spec.Egress) != 1 {
		t.Fatalf("expected network policy manifest, got %+v", client.manifest.NetworkPolicy)
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

type fakeKubernetesClient struct {
	manifest KubernetesManifest
	pod      *KubernetesPodStatus
}

func (f *fakeKubernetesClient) Apply(ctx context.Context, manifest KubernetesManifest) error {
	f.manifest = manifest
	return nil
}

func (f *fakeKubernetesClient) Delete(ctx context.Context, names KubernetesResourceNames) error {
	return nil
}

func (f *fakeKubernetesClient) GetPod(ctx context.Context, namespace, name string) (*KubernetesPodStatus, error) {
	return f.pod, nil
}

func (f *fakeKubernetesClient) Exec(ctx context.Context, namespace, name string, cmd ExecCommand) (*ExecResult, error) {
	return &ExecResult{ExitCode: 0}, nil
}

func (f *fakeKubernetesClient) StartPortForward(ctx context.Context, namespace, name string, localPort, remotePort int) (PortForwardHandle, error) {
	return fakePortForwardHandle("http://127.0.0.1"), nil
}

type fakePortForwardHandle string

func (f fakePortForwardHandle) Address() string { return string(f) }
func (f fakePortForwardHandle) Close() error    { return nil }
