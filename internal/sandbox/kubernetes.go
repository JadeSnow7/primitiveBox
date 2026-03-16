package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var ErrKubernetesResourceNotFound = errors.New("kubernetes resource not found")

const (
	defaultKubernetesNamespace    = "default"
	defaultKubernetesWorkspacePVC = "1Gi"
)

type SandboxLookup func(ctx context.Context, sandboxID string) (*Sandbox, bool, error)

// KubernetesClient is the minimal client surface required by the runtime driver.
type KubernetesClient interface {
	Apply(ctx context.Context, manifest KubernetesManifest) error
	Delete(ctx context.Context, names KubernetesResourceNames) error
	GetPod(ctx context.Context, namespace, name string) (*KubernetesPodStatus, error)
	Exec(ctx context.Context, namespace, name string, cmd ExecCommand) (*ExecResult, error)
	StartPortForward(ctx context.Context, namespace, name string, localPort, remotePort int) (PortForwardHandle, error)
}

// PortForwardHandle tracks a live local port-forward session.
type PortForwardHandle interface {
	Address() string
	Close() error
}

type KubernetesResourceNames struct {
	Namespace         string `json:"namespace"`
	PodName           string `json:"pod_name"`
	ServiceName       string `json:"service_name"`
	WorkspacePVCName  string `json:"workspace_pvc_name"`
	NetworkPolicyName string `json:"network_policy_name,omitempty"`
}

// KubernetesManifest represents the runtime objects needed for one sandbox.
type KubernetesManifest struct {
	Resources     KubernetesResourceNames       `json:"resources"`
	Pod           *corev1.Pod                   `json:"pod"`
	Service       *corev1.Service               `json:"service"`
	WorkspacePVC  *corev1.PersistentVolumeClaim `json:"workspace_pvc"`
	NetworkPolicy *networkingv1.NetworkPolicy   `json:"network_policy,omitempty"`
}

// KubernetesPodStatus is the runtime inspection view returned by the client.
type KubernetesPodStatus struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Phase       string            `json:"phase"`
	Ready       bool              `json:"ready"`
	PodIP       string            `json:"pod_ip,omitempty"`
	ContainerID string            `json:"container_id,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// KubernetesDriver schedules one pod per sandbox in a Kubernetes cluster.
type KubernetesDriver struct {
	client       KubernetesClient
	lookup       SandboxLookup
	defaultImage string

	mu           sync.Mutex
	portForwards map[string]PortForwardHandle
	ports        map[string]int
	namespaces   map[string]string
}

// NewKubernetesDriver creates a Kubernetes runtime driver.
func NewKubernetesDriver(client KubernetesClient) *KubernetesDriver {
	return &KubernetesDriver{
		client:       client,
		defaultImage: "primitivebox-sandbox:latest",
		portForwards: make(map[string]PortForwardHandle),
		ports:        make(map[string]int),
		namespaces:   make(map[string]string),
	}
}

// WithSandboxLookup injects a control-plane lookup used to recover namespace and metadata.
func (d *KubernetesDriver) WithSandboxLookup(lookup SandboxLookup) *KubernetesDriver {
	d.lookup = lookup
	return d
}

func (d *KubernetesDriver) Name() string {
	return "kubernetes"
}

func (d *KubernetesDriver) Capabilities() []RuntimeCapability {
	return []RuntimeCapability{
		{Name: "exec", Supported: true},
		{Name: "stream_exec", Supported: true},
		{Name: "ttl_reaper", Supported: true, Notes: "handled by host control plane"},
		{Name: "network_policy", Supported: true, Notes: "enforced via Kubernetes NetworkPolicy manifest"},
	}
}

func (d *KubernetesDriver) Create(ctx context.Context, config SandboxConfig) (*Sandbox, error) {
	if d.client == nil {
		return nil, fmt.Errorf("kubernetes client unavailable")
	}
	if config.MountSource != "" {
		return nil, fmt.Errorf("kubernetes driver does not support host mounts in v1; use a PVC-backed workspace")
	}
	if len(config.NetworkPolicy.AllowHosts) > 0 {
		return nil, fmt.Errorf("kubernetes driver does not support allow_hosts in v1; use allow_cidrs instead")
	}
	if config.Namespace == "" {
		config.Namespace = defaultKubernetesNamespace
	}

	image := config.Image
	if image == "" {
		image = d.defaultImage
	}

	sandboxID := generateID()
	port, err := reserveLocalPort()
	if err != nil {
		return nil, err
	}

	manifest := d.buildManifest(sandboxID, image, config)
	if err := d.client.Apply(ctx, manifest); err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.ports[sandboxID] = port
	d.namespaces[sandboxID] = config.Namespace
	d.mu.Unlock()

	return &Sandbox{
		ID:           sandboxID,
		ContainerID:  "pod/" + sandboxID,
		Driver:       d.Name(),
		Namespace:    config.Namespace,
		Config:       config,
		Status:       StatusCreating,
		HealthStatus: "starting",
		RPCPort:      port,
		RPCEndpoint:  fmt.Sprintf("http://127.0.0.1:%d", port),
		CreatedAt:    time.Now().UTC().Unix(),
		Labels:       copyStringMap(config.Labels),
		Capabilities: d.Capabilities(),
		Metadata: map[string]string{
			"pod_name":            manifest.Resources.PodName,
			"service_name":        manifest.Resources.ServiceName,
			"workspace_pvc_name":  manifest.Resources.WorkspacePVCName,
			"network_policy_name": manifest.Resources.NetworkPolicyName,
			"namespace":           manifest.Resources.Namespace,
			"driver":              d.Name(),
			"networking":          string(config.NetworkPolicy.Mode),
		},
	}, nil
}

func (d *KubernetesDriver) Start(ctx context.Context, sandboxID string) error {
	if d.client == nil {
		return fmt.Errorf("kubernetes client unavailable")
	}

	sb, err := d.loadSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	namespace := sb.Namespace
	if namespace == "" {
		namespace = defaultKubernetesNamespace
	}

	ready := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := d.client.GetPod(ctx, namespace, sandboxID)
		if statusErr != nil {
			if errors.Is(statusErr, ErrKubernetesResourceNotFound) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return statusErr
		}
		if status.Ready {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("sandbox %s pod did not become ready before timeout", sandboxID)
	}

	_, _, err = d.ensurePortForward(ctx, sandboxID, namespace, sb.RPCPort)
	if err != nil {
		return err
	}
	return nil
}

func (d *KubernetesDriver) Stop(ctx context.Context, sandboxID string) error {
	_ = ctx
	d.mu.Lock()
	defer d.mu.Unlock()
	if handle, ok := d.portForwards[sandboxID]; ok {
		delete(d.portForwards, sandboxID)
		return handle.Close()
	}
	return nil
}

func (d *KubernetesDriver) Destroy(ctx context.Context, sandboxID string) error {
	_ = d.Stop(ctx, sandboxID)
	if d.client == nil {
		return fmt.Errorf("kubernetes client unavailable")
	}

	sb, err := d.loadSandbox(ctx, sandboxID)
	if err != nil && !errors.Is(err, ErrKubernetesResourceNotFound) {
		return err
	}

	names := kubernetesResourceNames(sandboxID, defaultKubernetesNamespace)
	if sb != nil {
		names = kubernetesResourceNames(sandboxID, defaultString(sb.Namespace, defaultKubernetesNamespace))
	}

	d.mu.Lock()
	delete(d.ports, sandboxID)
	delete(d.namespaces, sandboxID)
	d.mu.Unlock()

	if err := d.client.Delete(ctx, names); err != nil && !errors.Is(err, ErrKubernetesResourceNotFound) {
		return err
	}
	return nil
}

func (d *KubernetesDriver) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	if d.client == nil {
		return nil, fmt.Errorf("kubernetes client unavailable")
	}
	sb, err := d.loadSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return d.client.Exec(ctx, defaultString(sb.Namespace, defaultKubernetesNamespace), sandboxID, cmd)
}

func (d *KubernetesDriver) Inspect(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if d.client == nil {
		return nil, fmt.Errorf("kubernetes client unavailable")
	}

	sb, loadErr := d.loadSandbox(ctx, sandboxID)
	namespace := defaultKubernetesNamespace
	if sb != nil && sb.Namespace != "" {
		namespace = sb.Namespace
	} else {
		d.mu.Lock()
		if ns, ok := d.namespaces[sandboxID]; ok && ns != "" {
			namespace = ns
		}
		d.mu.Unlock()
	}

	status, err := d.client.GetPod(ctx, namespace, sandboxID)
	if err != nil {
		if errors.Is(err, ErrKubernetesResourceNotFound) && sb != nil {
			return &Sandbox{
				ID:           sandboxID,
				Driver:       d.Name(),
				Namespace:    namespace,
				Config:       sb.Config,
				Status:       StatusDestroyed,
				HealthStatus: "destroyed",
				Labels:       copyStringMap(sb.Labels),
				Capabilities: d.Capabilities(),
				Metadata:     copyStringMap(sb.Metadata),
			}, nil
		}
		if loadErr != nil {
			return nil, loadErr
		}
		return nil, err
	}

	statusValue := mapPodStatus(status.Phase, status.Ready)
	inspected := &Sandbox{
		ID:           sandboxID,
		ContainerID:  status.ContainerID,
		Driver:       d.Name(),
		Namespace:    status.Namespace,
		Status:       statusValue,
		HealthStatus: "starting",
		Labels:       copyStringMap(status.Labels),
		Capabilities: d.Capabilities(),
		Metadata: map[string]string{
			"pod_ip": status.PodIP,
		},
	}
	if sb != nil {
		inspected.Config = sb.Config
		inspected.Metadata = copyStringMap(sb.Metadata)
		if inspected.Metadata == nil {
			inspected.Metadata = map[string]string{}
		}
		inspected.Metadata["pod_ip"] = status.PodIP
	}

	d.mu.Lock()
	if port, ok := d.ports[sandboxID]; ok {
		inspected.RPCPort = port
	}
	if handle, ok := d.portForwards[sandboxID]; ok {
		inspected.RPCEndpoint = handle.Address()
	}
	d.mu.Unlock()

	if inspected.Status == StatusRunning {
		handle, port, pfErr := d.ensurePortForward(ctx, sandboxID, namespace, inspected.RPCPort)
		if pfErr == nil {
			inspected.RPCPort = port
			inspected.RPCEndpoint = handle.Address()
		} else {
			inspected.RPCPort = 0
			inspected.RPCEndpoint = ""
		}
	}

	if inspected.Status == StatusRunning {
		inspected.HealthStatus = "healthy"
	}
	return inspected, nil
}

func (d *KubernetesDriver) Status(ctx context.Context, sandboxID string) (SandboxStatus, error) {
	sb, err := d.Inspect(ctx, sandboxID)
	if err != nil {
		return StatusError, err
	}
	return sb.Status, nil
}

func (d *KubernetesDriver) buildManifest(sandboxID, image string, config SandboxConfig) KubernetesManifest {
	names := kubernetesResourceNames(sandboxID, defaultString(config.Namespace, defaultKubernetesNamespace))
	labels := copyStringMap(config.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels["primitivebox.sandbox_id"] = sandboxID
	labels["managed-by"] = "primitivebox"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.PodName,
			Namespace: names.Namespace,
			Labels:    copyStringMap(labels),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            "pb-server",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command: []string{
					"sh",
					"-lc",
					fmt.Sprintf("pb-runtimed --host 0.0.0.0 --workspace %s --port %d --sandbox-id %s", defaultString(config.MountTarget, "/workspace"), containerRPCListen, sandboxID),
				},
				Env:                      envVarsFromMap(config.Env),
				WorkingDir:               defaultString(config.MountTarget, "/workspace"),
				Ports:                    []corev1.ContainerPort{{Name: "rpc", ContainerPort: containerRPCListen}},
				VolumeMounts:             []corev1.VolumeMount{{Name: "workspace", MountPath: defaultString(config.MountTarget, "/workspace")}},
				Resources:                buildKubernetesResources(config),
				ReadinessProbe:           httpProbe("/health", containerRPCListen),
				LivenessProbe:            httpProbe("/health", containerRPCListen),
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
			Volumes: []corev1.Volume{{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: names.WorkspacePVCName,
					},
				},
			}},
		},
	}
	applyUserToPodSpec(&pod.Spec, config.User)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.ServiceName,
			Namespace: names.Namespace,
			Labels:    copyStringMap(labels),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"primitivebox.sandbox_id": sandboxID},
			Ports: []corev1.ServicePort{{
				Name:       "rpc",
				Port:       int32(containerRPCListen),
				TargetPort: intstr.FromInt(containerRPCListen),
			}},
		},
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.WorkspacePVCName,
			Namespace: names.Namespace,
			Labels:    copyStringMap(labels),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: kubernetesStorageQuantity(config),
				},
			},
		},
	}

	manifest := KubernetesManifest{
		Resources:    names,
		Pod:          pod,
		Service:      service,
		WorkspacePVC: pvc,
	}
	if config.NetworkPolicy.Mode == NetworkModeNone || config.NetworkPolicy.Mode == NetworkModePolicy {
		manifest.NetworkPolicy = buildKubernetesNetworkPolicy(names, labels, config.NetworkPolicy)
	}
	return manifest
}

func (d *KubernetesDriver) loadSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if d.lookup != nil {
		if sb, ok, err := d.lookup(ctx, sandboxID); err != nil {
			return nil, err
		} else if ok {
			return sb, nil
		}
	}
	d.mu.Lock()
	namespace := d.namespaces[sandboxID]
	port := d.ports[sandboxID]
	d.mu.Unlock()
	if namespace == "" && port == 0 {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}
	return &Sandbox{
		ID:        sandboxID,
		Namespace: namespace,
		RPCPort:   port,
	}, nil
}

func (d *KubernetesDriver) ensurePortForward(ctx context.Context, sandboxID, namespace string, preferredPort int) (PortForwardHandle, int, error) {
	d.mu.Lock()
	if handle, ok := d.portForwards[sandboxID]; ok {
		port := d.ports[sandboxID]
		d.mu.Unlock()
		return handle, port, nil
	}
	d.mu.Unlock()

	portsToTry := make([]int, 0, 2)
	if preferredPort > 0 {
		portsToTry = append(portsToTry, preferredPort)
	}
	fallbackPort, err := reserveLocalPort()
	if err != nil {
		return nil, 0, err
	}
	if fallbackPort != preferredPort {
		portsToTry = append(portsToTry, fallbackPort)
	}

	var lastErr error
	for _, localPort := range portsToTry {
		handle, startErr := d.client.StartPortForward(ctx, namespace, sandboxID, localPort, containerRPCListen)
		if startErr != nil {
			lastErr = startErr
			continue
		}

		d.mu.Lock()
		if existing, ok := d.portForwards[sandboxID]; ok {
			_ = existing.Close()
		}
		d.portForwards[sandboxID] = handle
		d.ports[sandboxID] = localPort
		d.namespaces[sandboxID] = namespace
		d.mu.Unlock()
		return handle, localPort, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to establish port-forward for sandbox %s", sandboxID)
	}
	return nil, 0, lastErr
}

func kubernetesResourceNames(sandboxID, namespace string) KubernetesResourceNames {
	return KubernetesResourceNames{
		Namespace:         namespace,
		PodName:           sandboxID,
		ServiceName:       sandboxID,
		WorkspacePVCName:  sandboxID + "-workspace",
		NetworkPolicyName: sandboxID + "-network",
	}
}

func buildKubernetesResources(config SandboxConfig) corev1.ResourceRequirements {
	limits := corev1.ResourceList{}
	if config.CPULimit > 0 {
		limits[corev1.ResourceCPU] = apiresource.MustParse(strconv.FormatFloat(config.CPULimit, 'f', -1, 64))
	}
	if config.MemoryLimit > 0 {
		limits[corev1.ResourceMemory] = apiresource.MustParse(fmt.Sprintf("%dMi", config.MemoryLimit))
	}
	return corev1.ResourceRequirements{
		Requests: copyResourceList(limits),
		Limits:   limits,
	}
}

func copyResourceList(src corev1.ResourceList) corev1.ResourceList {
	if len(src) == 0 {
		return nil
	}
	out := make(corev1.ResourceList, len(src))
	for k, v := range src {
		out[k] = v.DeepCopy()
	}
	return out
}

func httpProbe(path string, port int) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt(port),
			},
		},
		InitialDelaySeconds: 1,
		PeriodSeconds:       2,
	}
}

func applyUserToPodSpec(spec *corev1.PodSpec, user string) {
	if spec == nil || user == "" {
		return
	}
	parts := strings.Split(user, ":")
	if len(parts) == 0 {
		return
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	securityContext := &corev1.PodSecurityContext{RunAsUser: &uid}
	if len(parts) > 1 {
		if gid, gidErr := strconv.ParseInt(parts[1], 10, 64); gidErr == nil {
			securityContext.RunAsGroup = &gid
			securityContext.FSGroup = &gid
		}
	}
	spec.SecurityContext = securityContext
}

func envVarsFromMap(env map[string]string) []corev1.EnvVar {
	if len(env) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(env))
	for key, value := range env {
		out = append(out, corev1.EnvVar{Name: key, Value: value})
	}
	return out
}

func buildKubernetesNetworkPolicy(names KubernetesResourceNames, labels map[string]string, policy NetworkPolicy) *networkingv1.NetworkPolicy {
	netPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.NetworkPolicyName,
			Namespace: names.Namespace,
			Labels:    copyStringMap(labels),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"primitivebox.sandbox_id": names.PodName},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
	if policy.Mode == NetworkModeNone {
		netPolicy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{}
		return netPolicy
	}

	egressRule := networkingv1.NetworkPolicyEgressRule{}
	for _, cidr := range policy.AllowCIDRs {
		egressRule.To = append(egressRule.To, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: cidr},
		})
	}
	for _, port := range policy.AllowPorts {
		portCopy := intstr.FromInt(port)
		protocol := corev1.ProtocolTCP
		egressRule.Ports = append(egressRule.Ports, networkingv1.NetworkPolicyPort{
			Protocol: &protocol,
			Port:     &portCopy,
		})
	}
	if len(egressRule.To) > 0 || len(egressRule.Ports) > 0 {
		netPolicy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{egressRule}
	}
	return netPolicy
}

func kubernetesStorageQuantity(config SandboxConfig) apiresource.Quantity {
	if config.DiskLimit <= 0 {
		return apiresource.MustParse(defaultKubernetesWorkspacePVC)
	}
	return apiresource.MustParse(fmt.Sprintf("%dMi", config.DiskLimit))
}

func mapPodStatus(phase string, ready bool) SandboxStatus {
	switch {
	case ready || phase == string(corev1.PodRunning):
		return StatusRunning
	case phase == string(corev1.PodSucceeded), phase == string(corev1.PodFailed):
		return StatusStopped
	case phase == "Deleted":
		return StatusDestroyed
	default:
		return StatusCreating
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
