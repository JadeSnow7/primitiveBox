package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

type defaultKubernetesClient struct {
	clientset  kubernetes.Interface
	restConfig *rest.Config
}

func NewDefaultKubernetesClient() (KubernetesClient, error) {
	restConfig, err := loadKubernetesConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return &defaultKubernetesClient{
		clientset:  clientset,
		restConfig: restConfig,
	}, nil
}

func loadKubernetesConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	return loader.ClientConfig()
}

func (c *defaultKubernetesClient) Apply(ctx context.Context, manifest KubernetesManifest) error {
	namespace := manifest.Resources.Namespace
	if err := c.applyPVC(ctx, namespace, manifest.WorkspacePVC); err != nil {
		return err
	}
	if err := c.applyPod(ctx, namespace, manifest.Pod); err != nil {
		return err
	}
	if err := c.applyService(ctx, namespace, manifest.Service); err != nil {
		return err
	}
	if manifest.NetworkPolicy != nil {
		if err := c.applyNetworkPolicy(ctx, namespace, manifest.NetworkPolicy); err != nil {
			return err
		}
	}
	return nil
}

func (c *defaultKubernetesClient) Delete(ctx context.Context, names KubernetesResourceNames) error {
	deletePolicy := metav1.DeletePropagationBackground
	if names.NetworkPolicyName != "" {
		if err := c.clientset.NetworkingV1().NetworkPolicies(names.Namespace).Delete(ctx, names.NetworkPolicyName, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := c.clientset.CoreV1().Services(names.Namespace).Delete(ctx, names.ServiceName, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.clientset.CoreV1().Pods(names.Namespace).Delete(ctx, names.PodName, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.clientset.CoreV1().PersistentVolumeClaims(names.Namespace).Delete(ctx, names.WorkspacePVCName, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *defaultKubernetesClient) GetPod(ctx context.Context, namespace, name string) (*KubernetesPodStatus, error) {
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrKubernetesResourceNotFound
		}
		return nil, err
	}
	status := &KubernetesPodStatus{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Phase:     string(pod.Status.Phase),
		PodIP:     pod.Status.PodIP,
		Labels:    copyStringMap(pod.Labels),
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			status.Ready = true
			break
		}
	}
	if len(pod.Status.ContainerStatuses) > 0 {
		status.ContainerID = pod.Status.ContainerStatuses[0].ContainerID
	}
	return status, nil
}

func (c *defaultKubernetesClient) Exec(ctx context.Context, namespace, name string, cmd ExecCommand) (*ExecResult, error) {
	command := append([]string{cmd.Command}, cmd.Args...)
	if len(command) == 0 || command[0] == "" {
		return nil, fmt.Errorf("exec command is required")
	}

	execCtx := ctx
	cancel := func() {}
	if cmd.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(cmd.Timeout)*time.Second)
	}
	defer cancel()

	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(name).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Command:   command,
		Container: "pb-server",
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	start := time.Now()
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	duration := time.Since(start)
	result := &ExecResult{
		ExitCode:   0,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: duration.Milliseconds(),
		TimedOut:   execCtx.Err() == context.DeadlineExceeded,
	}
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.ExitCode = -1
			return result, nil
		}
		return nil, err
	}
	return result, nil
}

func (c *defaultKubernetesClient) StartPortForward(ctx context.Context, namespace, name string, localPort, remotePort int) (PortForwardHandle, error) {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(name).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(c.restConfig)
	if err != nil {
		return nil, err
	}
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	pf, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("%d:%d", localPort, remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return nil, err
	}

	go func() {
		errCh <- pf.ForwardPorts()
	}()

	select {
	case <-readyCh:
		return &kubernetesPortForwardHandle{
			address: fmt.Sprintf("http://127.0.0.1:%d", localPort),
			stopCh:  stopCh,
			errCh:   errCh,
		}, nil
	case err := <-errCh:
		close(stopCh)
		return nil, err
	case <-ctx.Done():
		close(stopCh)
		return nil, ctx.Err()
	}
}

func (c *defaultKubernetesClient) applyPVC(ctx context.Context, namespace string, pvc *corev1.PersistentVolumeClaim) error {
	if pvc == nil {
		return nil
	}
	existing, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
			return err
		}
		return err
	}
	pvc.ResourceVersion = existing.ResourceVersion
	_, err = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})
	return err
}

func (c *defaultKubernetesClient) applyPod(ctx context.Context, namespace string, pod *corev1.Pod) error {
	existing, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = c.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Pod specs are largely immutable after creation. Kubernetes rejects updates to
	// most spec fields on running and pending pods. Only recreate if the existing
	// pod has reached a terminal phase (Succeeded/Failed) — otherwise treat the
	// running/pending pod as already satisfying the desired state.
	phase := existing.Status.Phase
	if phase != corev1.PodSucceeded && phase != corev1.PodFailed {
		return nil
	}

	deletePolicy := metav1.DeletePropagationBackground
	if err := c.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	_, err = c.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	return err
}

func (c *defaultKubernetesClient) applyService(ctx context.Context, namespace string, svc *corev1.Service) error {
	existing, err := c.clientset.CoreV1().Services(namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = c.clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
			return err
		}
		return err
	}
	svc.ResourceVersion = existing.ResourceVersion
	svc.Spec.ClusterIP = existing.Spec.ClusterIP
	svc.Spec.ClusterIPs = existing.Spec.ClusterIPs
	_, err = c.clientset.CoreV1().Services(namespace).Update(ctx, svc, metav1.UpdateOptions{})
	return err
}

func (c *defaultKubernetesClient) applyNetworkPolicy(ctx context.Context, namespace string, policy *networkingv1.NetworkPolicy) error {
	existing, err := c.clientset.NetworkingV1().NetworkPolicies(namespace).Get(ctx, policy.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = c.clientset.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{})
			return err
		}
		return err
	}
	policy.ResourceVersion = existing.ResourceVersion
	_, err = c.clientset.NetworkingV1().NetworkPolicies(namespace).Update(ctx, policy, metav1.UpdateOptions{})
	return err
}

type kubernetesPortForwardHandle struct {
	address string
	stopCh  chan struct{}
	errCh   chan error
	once    sync.Once
}

func (h *kubernetesPortForwardHandle) Address() string {
	return h.address
}

func (h *kubernetesPortForwardHandle) Close() error {
	h.once.Do(func() {
		close(h.stopCh)
	})
	select {
	case err := <-h.errCh:
		if err == nil || err == io.EOF {
			return nil
		}
		return err
	case <-time.After(2 * time.Second):
		return nil
	}
}
