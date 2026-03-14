package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	containerapi "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

const (
	containerRPCPort    = "8080/tcp"
	containerRPCListen  = 8080
	defaultContainerCmd = "sleep infinity"
)

// DockerDriver implements RuntimeDriver using Docker.
type DockerDriver struct {
	dockerClient *client.Client
	defaultImage string
	httpClient   *http.Client
}

// NewDockerDriver creates a new Docker-based runtime driver.
func NewDockerDriver() *DockerDriver {
	httpClient := &http.Client{Timeout: 3 * time.Second}
	return &DockerDriver{
		defaultImage: "primitivebox-sandbox:latest",
		httpClient:   httpClient,
	}
}

func (d *DockerDriver) Name() string {
	return "docker"
}

// Create provisions a new Docker container as a sandbox.
func (d *DockerDriver) Create(ctx context.Context, config SandboxConfig) (*Sandbox, error) {
	cli, err := d.client()
	if err != nil {
		return nil, err
	}

	image := config.Image
	if image == "" {
		image = d.defaultImage
	}

	hostPort, err := reserveLocalPort()
	if err != nil {
		return nil, fmt.Errorf("cannot allocate host port: %w", err)
	}

	portSet := nat.PortSet{
		nat.Port(containerRPCPort): struct{}{},
	}
	portBindings := nat.PortMap{
		nat.Port(containerRPCPort): []nat.PortBinding{{
			HostIP:   "127.0.0.1",
			HostPort: fmt.Sprintf("%d", hostPort),
		}},
	}

	sandboxID := generateID()
	labels := copyStringMap(config.Labels)
	labels["primitivebox.sandbox_id"] = sandboxID

	containerConfig := &containerapi.Config{
		Image:        image,
		User:         config.User,
		WorkingDir:   config.MountTarget,
		ExposedPorts: portSet,
		Cmd:          []string{"sh", "-lc", defaultContainerCmd},
		Labels:       labels,
	}

	hostConfig := &containerapi.HostConfig{
		NetworkMode: "none",
		Binds:       []string{fmt.Sprintf("%s:%s", config.MountSource, config.MountTarget)},
		Resources: containerapi.Resources{
			NanoCPUs: config.cpuQuota(),
			Memory:   config.memoryBytes(),
		},
		PortBindings: portBindings,
	}

	resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("docker container create failed: %w", err)
	}

	sandbox := &Sandbox{
		ID:           sandboxID,
		ContainerID:  resp.ID,
		Config:       config,
		Status:       StatusStopped,
		HealthStatus: "stopped",
		RPCPort:      hostPort,
		RPCEndpoint:  fmt.Sprintf("http://127.0.0.1:%d", hostPort),
		CreatedAt:    time.Now().Unix(),
		Labels:       copyStringMap(config.Labels),
	}

	return sandbox, nil
}

// Start activates a Docker container and the container-local pb server.
func (d *DockerDriver) Start(ctx context.Context, sandboxID string) error {
	cli, err := d.client()
	if err != nil {
		return err
	}

	sb, err := d.inspectSandbox(ctx, cli, sandboxID)
	if err != nil {
		return err
	}

	if err := cli.ContainerStart(ctx, sb.ContainerID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("docker container start failed: %w", err)
	}

	if _, err := d.Exec(ctx, sandboxID, ExecCommand{
		Command:    "sh",
		Args:       []string{"-lc", fmt.Sprintf("nohup pb server start --host 0.0.0.0 --workspace %s --port %d >/tmp/primitivebox-server.log 2>&1 &", sb.Config.MountTarget, containerRPCListen)},
		Timeout:    10,
		User:       sb.Config.User,
		WorkingDir: sb.Config.MountTarget,
	}); err != nil {
		return fmt.Errorf("failed to start pb server inside sandbox: %w", err)
	}

	healthURL := fmt.Sprintf("%s/health", sb.RPCEndpoint)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if reqErr != nil {
			return reqErr
		}
		resp, reqErr := d.httpClient.Do(req)
		if reqErr == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("sandbox %s did not become healthy in time", sandboxID)
}

// Stop gracefully stops a Docker container.
func (d *DockerDriver) Stop(ctx context.Context, sandboxID string) error {
	cli, err := d.client()
	if err != nil {
		return err
	}

	sb, err := d.inspectSandbox(ctx, cli, sandboxID)
	if err != nil {
		return err
	}

	timeout := 10
	if err := cli.ContainerStop(ctx, sb.ContainerID, containerapi.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("docker container stop failed: %w", err)
	}
	return nil
}

// Destroy removes a Docker container and its volumes.
func (d *DockerDriver) Destroy(ctx context.Context, sandboxID string) error {
	cli, err := d.client()
	if err != nil {
		return err
	}

	sb, err := d.inspectSandbox(ctx, cli, sandboxID)
	if err != nil {
		return err
	}

	if err := cli.ContainerRemove(ctx, sb.ContainerID, types.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		return fmt.Errorf("docker container remove failed: %w", err)
	}
	return nil
}

// Exec runs a command inside a Docker container.
func (d *DockerDriver) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	cli, err := d.client()
	if err != nil {
		return nil, err
	}

	sb, err := d.inspectSandbox(ctx, cli, sandboxID)
	if err != nil {
		return nil, err
	}

	command := append([]string{cmd.Command}, cmd.Args...)
	if len(command) == 0 || command[0] == "" {
		return nil, fmt.Errorf("exec command is required")
	}

	execConfig := types.ExecConfig{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   cmd.WorkingDir,
		User:         cmd.User,
	}
	if execConfig.WorkingDir == "" {
		execConfig.WorkingDir = sb.Config.MountTarget
	}
	if execConfig.User == "" {
		execConfig.User = sb.Config.User
	}
	if len(cmd.Env) > 0 {
		execConfig.Env = make([]string, 0, len(cmd.Env))
		for k, v := range cmd.Env {
			execConfig.Env = append(execConfig.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	timeout := 30 * time.Second
	if cmd.Timeout > 0 {
		timeout = time.Duration(cmd.Timeout) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	createResp, err := cli.ContainerExecCreate(execCtx, sb.ContainerID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("docker exec create failed: %w", err)
	}

	attachResp, err := cli.ContainerExecAttach(execCtx, createResp.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("docker exec attach failed: %w", err)
	}
	defer attachResp.Close()

	var stdout, stderr bytes.Buffer
	start := time.Now()
	_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	duration := time.Since(start)

	if copyErr != nil && !strings.Contains(copyErr.Error(), "use of closed network connection") {
		return nil, fmt.Errorf("docker exec stream failed: %w", copyErr)
	}

	inspect, err := cli.ContainerExecInspect(execCtx, createResp.ID)
	if err != nil {
		return nil, fmt.Errorf("docker exec inspect failed: %w", err)
	}

	timedOut := execCtx.Err() == context.DeadlineExceeded
	exitCode := inspect.ExitCode
	if timedOut {
		exitCode = -1
	}

	return &ExecResult{
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: duration.Milliseconds(),
		TimedOut:   timedOut,
	}, nil
}

// Status returns the current status of a Docker container.
func (d *DockerDriver) Status(ctx context.Context, sandboxID string) (SandboxStatus, error) {
	cli, err := d.client()
	if err != nil {
		return StatusError, err
	}

	sb, err := d.inspectSandbox(ctx, cli, sandboxID)
	if err != nil {
		return StatusError, err
	}

	inspect, err := cli.ContainerInspect(ctx, sb.ContainerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return StatusDestroyed, nil
		}
		return StatusError, fmt.Errorf("docker inspect failed: %w", err)
	}

	if inspect.State == nil {
		return StatusError, nil
	}

	switch {
	case inspect.State.Running:
		return StatusRunning, nil
	case inspect.State.Status == "created" || inspect.State.Status == "exited":
		return StatusStopped, nil
	case inspect.State.Dead:
		return StatusDestroyed, nil
	default:
		return StatusError, nil
	}
}

func (d *DockerDriver) client() (*client.Client, error) {
	if d.dockerClient != nil {
		return d.dockerClient, nil
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("cannot create docker client: %w", err)
	}

	d.dockerClient = cli
	return cli, nil
}

func (d *DockerDriver) inspectSandbox(ctx context.Context, cli *client.Client, sandboxID string) (*Sandbox, error) {
	containerID := sandboxID
	if strings.HasPrefix(sandboxID, "sb-") {
		containerID = ""
	}

	var inspect types.ContainerJSON
	var err error
	switch {
	case containerID != "":
		inspect, err = cli.ContainerInspect(ctx, containerID)
	default:
		containers, listErr := cli.ContainerList(ctx, types.ContainerListOptions{All: true})
		if listErr != nil {
			return nil, fmt.Errorf("docker list failed: %w", listErr)
		}
		for _, c := range containers {
			if c.Labels["primitivebox.sandbox_id"] == sandboxID {
				inspect, err = cli.ContainerInspect(ctx, c.ID)
				break
			}
		}
		if inspect.ContainerJSONBase == nil && err == nil {
			return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("docker inspect failed: %w", err)
	}

	sb := &Sandbox{
		ID:          sandboxID,
		ContainerID: inspect.ID,
		Status:      StatusStopped,
	}

	if inspect.Config != nil {
		sb.Config.Image = inspect.Config.Image
		sb.Config.User = inspect.Config.User
		sb.Config.MountTarget = inspect.Config.WorkingDir
		sb.Labels = copyStringMap(inspect.Config.Labels)
	}
	if inspect.HostConfig != nil {
		sb.Config.NetworkEnabled = inspect.HostConfig.NetworkMode != "none"
		sb.Config.MemoryLimit = inspect.HostConfig.Memory / (1024 * 1024)
		if inspect.HostConfig.NanoCPUs > 0 {
			sb.Config.CPULimit = float64(inspect.HostConfig.NanoCPUs) / 1e9
		}
	}
	for _, mount := range inspect.Mounts {
		if mount.Destination == "/workspace" {
			sb.Config.MountSource = mount.Source
			sb.Config.MountTarget = mount.Destination
			break
		}
	}

	if bindings, ok := inspect.NetworkSettings.Ports[nat.Port(containerRPCPort)]; ok && len(bindings) > 0 {
		sb.RPCEndpoint = fmt.Sprintf("http://127.0.0.1:%s", bindings[0].HostPort)
		fmt.Sscanf(bindings[0].HostPort, "%d", &sb.RPCPort)
	}

	return sb, nil
}

func reserveLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (c SandboxConfig) cpuQuota() int64 {
	if c.CPULimit <= 0 {
		return 0
	}
	return int64(c.CPULimit * 1e9)
}

func (c SandboxConfig) memoryBytes() int64 {
	if c.MemoryLimit <= 0 {
		return 0
	}
	return c.MemoryLimit * 1024 * 1024
}
