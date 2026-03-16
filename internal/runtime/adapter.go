package runtime

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"primitivebox/internal/primitive"
)

//go:embed manifests/*.json
var embeddedManifests embed.FS

type AdapterManifest struct {
	Name       string             `json:"name"`
	Version    string             `json:"version"`
	Transport  string             `json:"transport"`
	Command    string             `json:"command"`
	Args       []string           `json:"args"`
	Primitives []primitive.Schema `json:"primitives"`
}

type AdapterRegistration struct {
	Adapter   string `json:"adapter"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint,omitempty"`
	Socket    string `json:"socket,omitempty"`
	Pid       int    `json:"pid,omitempty"`
}

type AdapterProcess struct {
	manifest     AdapterManifest
	registration AdapterRegistration
	cmd          *exec.Cmd
	stderr       bytes.Buffer
}

func (r *Runtime) loadAdapters() error {
	manifests, err := loadManifests(r.config.AppsDir)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		process, err := startAdapterProcess(r.config, manifest)
		if err != nil {
			return err
		}
		r.adapters = append(r.adapters, process)
		client := newRemoteClient(process.registration)
		for _, schema := range manifest.Primitives {
			rawSchema := primitive.EnrichSchema(schema)
			rawSchema.Source = primitive.SourceApp
			rawSchema.Adapter = manifest.Name
			remote := &remotePrimitive{
				name:     rawSchema.Name,
				category: rawSchema.Namespace,
				schema:   rawSchema,
				client:   client,
			}
			if err := r.registerRawPrimitive(remote, rawSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadManifests(appsDir string) ([]AdapterManifest, error) {
	var manifests []AdapterManifest
	seen := map[string]bool{}
	if appsDir != "" {
		entries, _ := os.ReadDir(appsDir)
		for _, entry := range entries {
			var manifestPath string
			if entry.IsDir() {
				manifestPath = filepath.Join(appsDir, entry.Name(), "manifest.json")
			} else if strings.HasSuffix(entry.Name(), ".json") {
				manifestPath = filepath.Join(appsDir, entry.Name())
			}
			if manifestPath == "" {
				continue
			}
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				continue
			}
			var manifest AdapterManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				return nil, err
			}
			manifests = append(manifests, manifest)
			seen[manifest.Name] = true
		}
	}

	defaults, err := embeddedManifests.ReadDir("manifests")
	if err != nil {
		return nil, err
	}
	for _, entry := range defaults {
		data, err := embeddedManifests.ReadFile(filepath.Join("manifests", entry.Name()))
		if err != nil {
			return nil, err
		}
		var manifest AdapterManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, err
		}
		if !seen[manifest.Name] {
			manifests = append(manifests, manifest)
		}
	}
	return manifests, nil
}

func startAdapterProcess(config Config, manifest AdapterManifest) (*AdapterProcess, error) {
	command, err := resolveAdapterCommand(manifest.Command)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, len(manifest.Args))
	for _, arg := range manifest.Args {
		arg = strings.ReplaceAll(arg, "{{workspace}}", config.WorkspaceDir)
		args = append(args, arg)
	}

	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(),
		"PRIMITIVEBOX_WORKSPACE="+config.WorkspaceDir,
		"PRIMITIVEBOX_APPS_DIR="+config.AppsDir,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	process := &AdapterProcess{manifest: manifest, cmd: cmd}
	cmd.Stderr = &process.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(stdout)
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			errCh <- readErr
			return
		}
		lineCh <- strings.TrimSpace(line)
	}()

	select {
	case line := <-lineCh:
		var reg AdapterRegistration
		if err := json.Unmarshal([]byte(line), &reg); err != nil {
			return nil, fmt.Errorf("invalid adapter registration for %s: %w", manifest.Name, err)
		}
		reg.Pid = cmd.Process.Pid
		process.registration = reg
		return process, nil
	case err := <-errCh:
		return nil, fmt.Errorf("adapter %s failed to register: %w (%s)", manifest.Name, err, process.stderr.String())
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("adapter %s registration timed out", manifest.Name)
	}
}

func resolveAdapterCommand(command string) (string, error) {
	if resolved, err := exec.LookPath(command); err == nil {
		return resolved, nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve adapter command %s: %w", command, err)
	}
	candidate := filepath.Join(filepath.Dir(exePath), command)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("adapter command not found: %s", command)
}

func (p *AdapterProcess) Close() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "finished") {
		return err
	}
	_, _ = p.cmd.Process.Wait()
	return nil
}

type remotePrimitive struct {
	name     string
	category string
	schema   primitive.Schema
	client   *remoteClient
}

func (p *remotePrimitive) Name() string     { return p.name }
func (p *remotePrimitive) Category() string { return p.category }
func (p *remotePrimitive) Schema() primitive.Schema {
	return primitive.EnrichSchema(p.schema)
}
func (p *remotePrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	return p.client.Call(ctx, p.name, params)
}

type remoteClient struct {
	registration AdapterRegistration
	httpClient   *http.Client
}

func newRemoteClient(reg AdapterRegistration) *remoteClient {
	client := &http.Client{Timeout: 120 * time.Second}
	if reg.Transport == "unix" {
		socketPath := reg.Socket
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}
		client.Transport = transport
	}
	return &remoteClient{
		registration: reg,
		httpClient:   client,
	}
}

func (c *remoteClient) Call(ctx context.Context, method string, params json.RawMessage) (primitive.Result, error) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(params),
		"id":      "adapter-call",
	})
	url := c.registration.Endpoint + "/rpc"
	if c.registration.Transport == "unix" {
		url = "http://unix/rpc"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return primitive.Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return primitive.Result{}, fmt.Errorf("adapter %s unavailable: %w", c.registration.Adapter, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return primitive.Result{}, err
	}
	var rpcResp struct {
		Result primitive.Result `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(responseBody, &rpcResp); err != nil {
		return primitive.Result{}, err
	}
	if rpcResp.Error != nil {
		return primitive.Result{}, &primitive.PrimitiveError{Code: primitive.ErrExecution, Message: rpcResp.Error.Message}
	}
	return rpcResp.Result, nil
}
