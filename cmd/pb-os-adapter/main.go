package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

const defaultAppID = "pb-os-adapter"

type adapterConfig struct {
	socketPath  string
	rpcEndpoint string
	appID       string
	noRegister  bool
}

type adapterState struct {
	registry *processRegistry
}

type processRegistry struct {
	mu        sync.RWMutex
	counter   atomic.Uint64
	processes map[string]*processRecord
}

type processRecord struct {
	processID  string
	pid        int
	command    []string
	workingDir string
	startedAt  time.Time

	mu       sync.RWMutex
	state    string
	exitCode *int
	signal   string
	exitedAt *time.Time
	doneCh   chan struct{}
	doneOnce sync.Once
	proc     *os.Process
}

type appRPCRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type appRPCResponse struct {
	ID     any          `json:"id"`
	Result any          `json:"result,omitempty"`
	Error  *appRPCError `json:"error,omitempty"`
}

type appRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type httpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type httpRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

type processSummary struct {
	ProcessID string   `json:"process_id"`
	PID       int      `json:"pid"`
	Command   []string `json:"command"`
	StartedAt string   `json:"started_at"`
	State     string   `json:"state"`
	ExitCode  *int     `json:"exit_code"`
	Signal    string   `json:"signal,omitempty"`
	ExitedAt  *string  `json:"exited_at,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() adapterConfig {
	cfg := adapterConfig{}
	flag.StringVar(&cfg.socketPath, "socket", filepath.Join(os.TempDir(), "pb-os.sock"), "Unix socket path for process primitive dispatch")
	flag.StringVar(&cfg.rpcEndpoint, "rpc-endpoint", "", "Sandbox-local PrimitiveBox HTTP endpoint used for app.register")
	flag.StringVar(&cfg.appID, "app-id", defaultAppID, "Override app_id for registered primitives")
	flag.BoolVar(&cfg.noRegister, "no-register", false, "Skip app.register calls and only serve the Unix socket")
	flag.Parse()
	return cfg
}

func run(cfg adapterConfig) error {
	listener, err := listenUnix(cfg.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	state := &adapterState{registry: newProcessRegistry()}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, listener, state)
	}()

	if !cfg.noRegister {
		if strings.TrimSpace(cfg.rpcEndpoint) == "" {
			return errors.New("--rpc-endpoint is required unless --no-register is set")
		}
		for _, manifest := range buildManifestSet(cfg.appID, cfg.socketPath) {
			if err := registerPrimitive(ctx, cfg.rpcEndpoint, manifest); err != nil {
				return fmt.Errorf("register %s: %w", manifest.Name, err)
			}
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}
}

func buildManifestSet(appID, socketPath string) []primitive.AppPrimitiveManifest {
	queryIntent := cvr.PrimitiveIntent{
		Category:       cvr.IntentQuery,
		Reversible:     true,
		RiskLevel:      cvr.RiskLow,
		AffectedScopes: []string{"app:os"},
	}
	mutationMedium := cvr.PrimitiveIntent{
		Category:       cvr.IntentMutation,
		Reversible:     false,
		RiskLevel:      cvr.RiskMedium,
		AffectedScopes: []string{"app:os"},
	}
	mutationHigh := cvr.PrimitiveIntent{
		Category:       cvr.IntentMutation,
		Reversible:     false,
		RiskLevel:      cvr.RiskHigh,
		AffectedScopes: []string{"app:os"},
	}

	return []primitive.AppPrimitiveManifest{
		{
			AppID:       appID,
			Name:        "process.list",
			Description: "List adapter-managed OS processes known to pb-os-adapter.",
			InputSchema: mustJSON(map[string]any{"type": "object", "additionalProperties": false}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"processes": map[string]any{
						"type":  "array",
						"items": processSummarySchema(),
					},
				},
				"required":             []string{"processes"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryIntent,
		},
		{
			AppID:       appID,
			Name:        "process.spawn",
			Description: "Spawn a new adapter-managed OS process with explicit argv.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":     "array",
						"minItems": 1,
						"items":    map[string]any{"type": "string"},
					},
					"cwd": map[string]any{"type": "string"},
					"env": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "string"},
					},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string"},
					"pid":        map[string]any{"type": "integer"},
					"command": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"started_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"required":             []string{"process_id", "pid", "command", "started_at"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationMedium,
		},
		{
			AppID:       appID,
			Name:        "process.wait",
			Description: "Wait for an adapter-managed process to exit or time out.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string"},
					"timeout_s":  map[string]any{"type": "number", "minimum": 0},
				},
				"required":             []string{"process_id"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string"},
					"pid":        map[string]any{"type": "integer"},
					"exited":     map[string]any{"type": "boolean"},
					"running":    map[string]any{"type": "boolean"},
					"exit_code":  map[string]any{"type": "integer"},
					"signal":     map[string]any{"type": "string"},
					"timed_out":  map[string]any{"type": "boolean"},
					"started_at": map[string]any{"type": "string", "format": "date-time"},
					"exited_at":  map[string]any{"type": "string", "format": "date-time"},
				},
				"required":             []string{"process_id", "pid", "exited", "running", "timed_out", "started_at"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryIntent,
		},
		{
			AppID:       appID,
			Name:        "process.terminate",
			Description: "Send SIGTERM to an adapter-managed process.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string"},
				},
				"required":             []string{"process_id"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id":     map[string]any{"type": "string"},
					"pid":            map[string]any{"type": "integer"},
					"terminated":     map[string]any{"type": "boolean"},
					"already_exited": map[string]any{"type": "boolean"},
					"signal_sent":    map[string]any{"type": "string"},
				},
				"required":             []string{"process_id", "pid", "terminated", "already_exited", "signal_sent"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationMedium,
		},
		{
			AppID:       appID,
			Name:        "process.kill",
			Description: "Send SIGKILL to an adapter-managed process.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string"},
				},
				"required":             []string{"process_id"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"process_id":     map[string]any{"type": "string"},
					"pid":            map[string]any{"type": "integer"},
					"killed":         map[string]any{"type": "boolean"},
					"already_exited": map[string]any{"type": "boolean"},
					"signal_sent":    map[string]any{"type": "string"},
				},
				"required":             []string{"process_id", "pid", "killed", "already_exited", "signal_sent"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationHigh,
		},
		// -----------------------------------------------------------------
		// service.* — system service lifecycle primitives
		// -----------------------------------------------------------------
		{
			AppID:       appID,
			Name:        "service.status",
			Description: "Check whether a named system service is running (systemctl/launchctl).",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    map[string]any{"type": "string"},
					"running": map[string]any{"type": "boolean"},
					"status":  map[string]any{"type": "string"},
					"output":  map[string]any{"type": "string"},
				},
				"required":             []string{"name", "running", "status"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryIntent,
		},
		{
			AppID:       appID,
			Name:        "service.start",
			Description: "Start a named system service (systemctl/launchctl). Verifies via service.status.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    map[string]any{"type": "string"},
					"started": map[string]any{"type": "boolean"},
					"output":  map[string]any{"type": "string"},
				},
				"required":             []string{"name", "started"},
				"additionalProperties": false,
			}),
			SocketPath:     socketPath,
			Intent:         mutationMedium,
			VerifyEndpoint: "service.status",
		},
		{
			AppID:       appID,
			Name:        "service.stop",
			Description: "Stop a named system service (systemctl/launchctl). Rollback via service.start.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    map[string]any{"type": "string"},
					"stopped": map[string]any{"type": "boolean"},
					"output":  map[string]any{"type": "string"},
				},
				"required":             []string{"name", "stopped"},
				"additionalProperties": false,
			}),
			SocketPath:       socketPath,
			Intent:           mutationMedium,
			RollbackEndpoint: "service.start",
		},
		{
			AppID:       appID,
			Name:        "service.restart",
			Description: "Restart a named system service: stop then start with inline status verification.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      map[string]any{"type": "string"},
					"restarted": map[string]any{"type": "boolean"},
					"running":   map[string]any{"type": "boolean"},
					"output":    map[string]any{"type": "string"},
				},
				"required":             []string{"name", "restarted", "running"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationMedium,
		},
		// -----------------------------------------------------------------
		// pkg.* — system package management primitives
		// -----------------------------------------------------------------
		{
			AppID:       appID,
			Name:        "pkg.list",
			Description: "List installed system packages (dpkg/brew list).",
			InputSchema: mustJSON(map[string]any{"type": "object", "additionalProperties": false}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"packages": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required":             []string{"packages"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryIntent,
		},
		{
			AppID:       appID,
			Name:        "pkg.install",
			Description: "Install a system package (apt-get/brew). Irreversible — CVR auto-checkpoints. Verify via pkg.verify; rollback via pkg.remove.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      map[string]any{"type": "string"},
					"installed": map[string]any{"type": "boolean"},
					"output":    map[string]any{"type": "string"},
				},
				"required":             []string{"name", "installed"},
				"additionalProperties": false,
			}),
			SocketPath:       socketPath,
			Intent:           mutationHigh,
			VerifyEndpoint:   "pkg.verify",
			RollbackEndpoint: "pkg.remove",
		},
		{
			AppID:       appID,
			Name:        "pkg.remove",
			Description: "Remove a system package (apt-get/brew). High risk — escalation required.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    map[string]any{"type": "string"},
					"removed": map[string]any{"type": "boolean"},
					"output":  map[string]any{"type": "string"},
				},
				"required":             []string{"name", "removed"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationHigh,
		},
		{
			AppID:       appID,
			Name:        "pkg.verify",
			Description: "Verify a package is correctly installed (dpkg --verify / brew list <name>).",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":     map[string]any{"type": "string"},
					"verified": map[string]any{"type": "boolean"},
					"output":   map[string]any{"type": "string"},
				},
				"required":             []string{"name", "verified"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryIntent,
		},
	}
}

func processSummarySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"process_id": map[string]any{"type": "string"},
			"pid":        map[string]any{"type": "integer"},
			"command": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"started_at": map[string]any{"type": "string", "format": "date-time"},
			"state":      map[string]any{"type": "string"},
			"exit_code":  map[string]any{"type": "integer"},
			"signal":     map[string]any{"type": "string"},
			"exited_at":  map[string]any{"type": "string", "format": "date-time"},
		},
		"required":             []string{"process_id", "pid", "command", "started_at", "state"},
		"additionalProperties": false,
	}
}

func newProcessRegistry() *processRegistry {
	return &processRegistry{processes: make(map[string]*processRecord)}
}

func (r *processRegistry) spawn(command []string, cwd string, env map[string]string) (*processRecord, error) {
	if len(command) == 0 {
		return nil, errors.New("command is required")
	}
	if strings.TrimSpace(command[0]) == "" {
		return nil, errors.New("command[0] is required")
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = strings.TrimSpace(cwd)
	if len(env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), env)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	record := &processRecord{
		processID:  r.nextProcessID(),
		pid:        cmd.Process.Pid,
		command:    append([]string(nil), command...),
		workingDir: cmd.Dir,
		startedAt:  time.Now().UTC(),
		state:      "running",
		doneCh:     make(chan struct{}),
		proc:       cmd.Process,
	}

	r.mu.Lock()
	r.processes[record.processID] = record
	r.mu.Unlock()

	go r.reap(record, cmd)
	return record, nil
}

func (r *processRegistry) nextProcessID() string {
	id := r.counter.Add(1)
	return fmt.Sprintf("proc-%d", id)
}

func (r *processRegistry) reap(record *processRecord, cmd *exec.Cmd) {
	err := cmd.Wait()
	exitedAt := time.Now().UTC()

	record.mu.Lock()
	defer record.mu.Unlock()

	record.state = "exited"
	record.exitedAt = &exitedAt
	record.exitCode = nil
	record.signal = ""

	if err == nil {
		code := 0
		record.exitCode = &code
		record.finish()
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status := exitErr.ProcessState.ExitCode()
		record.exitCode = &status
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				record.signal = signalName(ws.Signal())
			}
		}
		record.finish()
		return
	}

	code := -1
	record.exitCode = &code
	record.finish()
}

func (r *processRegistry) get(processID string) (*processRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.processes[processID]
	return rec, ok
}

func (r *processRegistry) list() []processSummary {
	r.mu.RLock()
	records := make([]*processRecord, 0, len(r.processes))
	for _, rec := range r.processes {
		records = append(records, rec)
	}
	r.mu.RUnlock()

	items := make([]processSummary, 0, len(records))
	for _, rec := range records {
		items = append(items, rec.summary())
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt == items[j].StartedAt {
			return items[i].ProcessID < items[j].ProcessID
		}
		return items[i].StartedAt < items[j].StartedAt
	})
	return items
}

func (p *processRecord) finish() {
	p.doneOnce.Do(func() { close(p.doneCh) })
}

func (p *processRecord) isRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state == "running"
}

func (p *processRecord) summary() processSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var exitedAt *string
	if p.exitedAt != nil {
		value := p.exitedAt.Format(time.RFC3339Nano)
		exitedAt = &value
	}

	return processSummary{
		ProcessID: p.processID,
		PID:       p.pid,
		Command:   append([]string(nil), p.command...),
		StartedAt: p.startedAt.Format(time.RFC3339Nano),
		State:     p.state,
		ExitCode:  cloneIntPointer(p.exitCode),
		Signal:    p.signal,
		ExitedAt:  exitedAt,
	}
}

func (p *processRecord) waitResult(timedOut bool) map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := map[string]any{
		"process_id": p.processID,
		"pid":        p.pid,
		"exited":     p.state == "exited",
		"running":    p.state == "running",
		"timed_out":  timedOut,
		"started_at": p.startedAt.Format(time.RFC3339Nano),
	}
	if p.exitCode != nil {
		out["exit_code"] = *p.exitCode
	}
	if p.signal != "" {
		out["signal"] = p.signal
	}
	if p.exitedAt != nil {
		out["exited_at"] = p.exitedAt.Format(time.RFC3339Nano)
	}
	return out
}

func (p *processRecord) sendSignal(sig syscall.Signal) (bool, error) {
	p.mu.RLock()
	running := p.state == "running"
	proc := p.proc
	p.mu.RUnlock()

	if !running {
		return false, nil
	}
	if proc == nil {
		return false, errors.New("process handle unavailable")
	}
	if err := proc.Signal(sig); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

func serve(ctx context.Context, listener net.Listener, state *adapterState) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}
		go handleConn(ctx, conn, state)
	}
}

func handleConn(ctx context.Context, conn net.Conn, state *adapterState) {
	defer conn.Close()
	var req appRPCRequest
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = writeAppResponse(conn, appRPCResponse{
				ID:    req.ID,
				Error: &appRPCError{Code: -32603, Message: fmt.Sprintf("internal adapter error: %v", recovered)},
			})
		}
	}()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	result, rpcErr := dispatch(ctx, state, req.Method, req.Params)
	resp := appRPCResponse{ID: req.ID, Result: result}
	if rpcErr != nil {
		resp.Result = nil
		resp.Error = rpcErr
	}
	_ = writeAppResponse(conn, resp)
}

func dispatch(ctx context.Context, state *adapterState, method string, raw json.RawMessage) (any, *appRPCError) {
	switch method {
	case "process.list":
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		var in map[string]any
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, invalidParams("invalid request")
		}
		if len(in) != 0 {
			return nil, invalidParams("process.list accepts no parameters")
		}
		return map[string]any{"processes": state.registry.list()}, nil
	case "process.spawn":
		var in struct {
			Command []string          `json:"command"`
			CWD     string            `json:"cwd"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, invalidParams("command is required")
		}
		record, err := state.registry.spawn(in.Command, in.CWD, in.Env)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		return map[string]any{
			"process_id": record.processID,
			"pid":        record.pid,
			"command":    append([]string(nil), record.command...),
			"started_at": record.startedAt.Format(time.RFC3339Nano),
		}, nil
	case "process.wait":
		var in struct {
			ProcessID string  `json:"process_id"`
			TimeoutS  float64 `json:"timeout_s"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.ProcessID) == "" {
			return nil, invalidParams("process_id is required")
		}
		record, ok := state.registry.get(strings.TrimSpace(in.ProcessID))
		if !ok {
			return nil, invalidParams("unknown process_id: " + strings.TrimSpace(in.ProcessID))
		}
		if !record.isRunning() {
			return record.waitResult(false), nil
		}
		timeout, err := normalizeTimeout(in.TimeoutS)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		if timeout == 0 {
			select {
			case <-record.doneCh:
				return record.waitResult(false), nil
			case <-ctx.Done():
				return nil, &appRPCError{Code: -32603, Message: ctx.Err().Error()}
			}
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-record.doneCh:
			return record.waitResult(false), nil
		case <-timer.C:
			return record.waitResult(true), nil
		case <-ctx.Done():
			return nil, &appRPCError{Code: -32603, Message: ctx.Err().Error()}
		}
	case "process.terminate":
		return handleSignalPrimitive(state.registry, raw, syscall.SIGTERM, "terminated")
	case "process.kill":
		return handleSignalPrimitive(state.registry, raw, syscall.SIGKILL, "killed")
	case "service.status":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handleServiceStatus(ctx, strings.TrimSpace(in.Name))
	case "service.start":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handleServiceAction(ctx, strings.TrimSpace(in.Name), "start")
	case "service.stop":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handleServiceAction(ctx, strings.TrimSpace(in.Name), "stop")
	case "service.restart":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handleServiceRestart(ctx, strings.TrimSpace(in.Name))
	case "pkg.list":
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		var in map[string]any
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, invalidParams("invalid request")
		}
		if len(in) != 0 {
			return nil, invalidParams("pkg.list accepts no parameters")
		}
		return handlePkgList(ctx)
	case "pkg.install":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handlePkgInstall(ctx, strings.TrimSpace(in.Name))
	case "pkg.remove":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handlePkgRemove(ctx, strings.TrimSpace(in.Name))
	case "pkg.verify":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Name) == "" {
			return nil, invalidParams("name is required")
		}
		return handlePkgVerify(ctx, strings.TrimSpace(in.Name))
	default:
		return nil, &appRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

func handleSignalPrimitive(registry *processRegistry, raw json.RawMessage, sig syscall.Signal, resultKey string) (any, *appRPCError) {
	var in struct {
		ProcessID string `json:"process_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.ProcessID) == "" {
		return nil, invalidParams("process_id is required")
	}
	processID := strings.TrimSpace(in.ProcessID)
	record, ok := registry.get(processID)
	if !ok {
		return nil, invalidParams("unknown process_id: " + processID)
	}

	sent, err := record.sendSignal(sig)
	if err != nil {
		return nil, &appRPCError{Code: -32603, Message: err.Error()}
	}

	result := map[string]any{
		"process_id":     record.processID,
		"pid":            record.pid,
		"already_exited": !sent,
		"signal_sent":    "",
		resultKey:        sent,
	}
	if sent {
		result["signal_sent"] = signalName(sig)
	}
	return result, nil
}

func invalidParams(message string) *appRPCError {
	return &appRPCError{Code: -32602, Message: message}
}

func normalizeTimeout(value float64) (time.Duration, error) {
	if value < 0 {
		return 0, errors.New("timeout_s must be >= 0")
	}
	if value == 0 {
		return 0, nil
	}
	if value > 30 {
		return 0, errors.New("timeout_s must be <= 30")
	}
	return time.Duration(value * float64(time.Second)), nil
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return sig.String()
	}
}

func isLinux() bool {
	return runtime.GOOS == "linux"
}

// ---------------------------------------------------------------------------
// service.* handlers
// ---------------------------------------------------------------------------

func handleServiceStatus(ctx context.Context, name string) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", name)
		out, err := cmd.CombinedOutput()
		running := err == nil
		status := "stopped"
		if running {
			status = "active"
		}
		return map[string]any{
			"name":    name,
			"passed":  running,
			"running": running,
			"status":  status,
			"output":  strings.TrimSpace(string(out)),
		}, nil
	}
	// macOS: parse `brew services list`
	listCmd := exec.CommandContext(ctx, "brew", "services", "list")
	out, err := listCmd.CombinedOutput()
	running := false
	status := "stopped"
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == name {
				running = fields[1] == "started"
				status = fields[1]
				break
			}
		}
	}
	return map[string]any{
		"name":    name,
		"passed":  running,
		"running": running,
		"status":  status,
		"output":  strings.TrimSpace(string(out)),
	}, nil
}

func handleServiceAction(ctx context.Context, name, action string) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "systemctl", action, name)
	} else {
		cmd = exec.CommandContext(ctx, "brew", "services", action, name)
	}
	out, err := cmd.CombinedOutput()
	resultKey := action + "ed"
	switch action {
	case "start":
		resultKey = "started"
	case "stop":
		resultKey = "stopped"
	}
	return map[string]any{
		"name":    name,
		resultKey: err == nil,
		"output":  strings.TrimSpace(string(out)),
	}, nil
}

func handleServiceRestart(ctx context.Context, name string) (any, *appRPCError) {
	var stopCmd, startCmd *exec.Cmd
	if isLinux() {
		stopCmd = exec.CommandContext(ctx, "systemctl", "stop", name)
		startCmd = exec.CommandContext(ctx, "systemctl", "start", name)
	} else {
		stopCmd = exec.CommandContext(ctx, "brew", "services", "stop", name)
		startCmd = exec.CommandContext(ctx, "brew", "services", "start", name)
	}
	stopOut, _ := stopCmd.CombinedOutput()
	startOut, startErr := startCmd.CombinedOutput()

	statusResult, _ := handleServiceStatus(ctx, name)
	running := false
	if m, ok := statusResult.(map[string]any); ok {
		running, _ = m["running"].(bool)
	}

	combinedOutput := strings.TrimSpace(string(stopOut)) + "\n" + strings.TrimSpace(string(startOut))
	return map[string]any{
		"name":      name,
		"restarted": startErr == nil,
		"running":   running,
		"output":    strings.TrimSpace(combinedOutput),
	}, nil
}

// ---------------------------------------------------------------------------
// pkg.* handlers
// ---------------------------------------------------------------------------

func handlePkgList(ctx context.Context) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "dpkg", "--get-selections")
	} else {
		cmd = exec.CommandContext(ctx, "brew", "list", "--formula")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, &appRPCError{Code: -32603, Message: "pkg.list failed: " + err.Error()}
	}
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isLinux() {
			// dpkg --get-selections format: "packagename\tinstall"
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == "install" {
				packages = append(packages, fields[0])
			}
		} else {
			packages = append(packages, line)
		}
	}
	if packages == nil {
		packages = []string{}
	}
	return map[string]any{"packages": packages}, nil
}

func handlePkgInstall(ctx context.Context, name string) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "apt-get", "install", "-y", name)
	} else {
		cmd = exec.CommandContext(ctx, "brew", "install", name)
	}
	out, err := cmd.CombinedOutput()
	return map[string]any{
		"name":      name,
		"installed": err == nil,
		"output":    strings.TrimSpace(string(out)),
	}, nil
}

func handlePkgRemove(ctx context.Context, name string) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "apt-get", "remove", "-y", name)
	} else {
		cmd = exec.CommandContext(ctx, "brew", "uninstall", name)
	}
	out, err := cmd.CombinedOutput()
	return map[string]any{
		"name":    name,
		"removed": err == nil,
		"output":  strings.TrimSpace(string(out)),
	}, nil
}

func handlePkgVerify(ctx context.Context, name string) (any, *appRPCError) {
	var cmd *exec.Cmd
	if isLinux() {
		cmd = exec.CommandContext(ctx, "dpkg", "--verify", name)
	} else {
		cmd = exec.CommandContext(ctx, "brew", "list", name)
	}
	out, err := cmd.CombinedOutput()
	verified := err == nil
	return map[string]any{
		"name":     name,
		"passed":   verified,
		"verified": verified,
		"output":   strings.TrimSpace(string(out)),
	}, nil
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	values := make(map[string]string, len(base)+len(overrides))
	for _, item := range base {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func writeAppResponse(w io.Writer, resp appRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func registerPrimitive(ctx context.Context, endpoint string, manifest primitive.AppPrimitiveManifest) error {
	body, err := json.Marshal(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  "app.register",
		Params:  mustJSON(manifest),
		ID:      "register-" + manifest.Name,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var rpcResp httpRPCResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return fmt.Errorf("decode register response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
