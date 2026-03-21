package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"primitivebox/internal/primitive"
)

// DriverResolver maps a sandbox ID back to the runtime name that owns it.
type DriverResolver func(sandboxID string) (string, bool)

var ErrPrimitiveNotFound = errors.New("primitive not found")

const defaultAppPrimitiveTimeout = 30 * time.Second

// Router dispatches primitive execution to either built-in primitives or
// app-registered Unix socket handlers.
type Router struct {
	registry *primitive.Registry

	mu          sync.RWMutex
	appRegistry primitive.AppPrimitiveRegistry
	requestID   atomic.Uint64
}

func NewRouter(registry *primitive.Registry) *Router {
	return &Router{registry: registry}
}

func (r *Router) RegisterAppRegistry(reg primitive.AppPrimitiveRegistry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appRegistry = reg
}

func (r *Router) Route(ctx context.Context, method string, params json.RawMessage) (primitive.Result, error) {
	if r.registry != nil {
		if p, ok := r.registry.Get(method); ok {
			return p.Execute(ctx, params)
		}
	}

	appRegistry := r.currentAppRegistry()
	if appRegistry == nil {
		return primitive.Result{}, ErrPrimitiveNotFound
	}

	manifest, err := appRegistry.Get(ctx, method)
	if err != nil {
		return primitive.Result{}, err
	}
	if manifest == nil {
		return primitive.Result{}, ErrPrimitiveNotFound
	}

	return r.routeAppPrimitive(ctx, *manifest, method, params)
}

func (r *Router) currentAppRegistry() primitive.AppPrimitiveRegistry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.appRegistry
}

func (r *Router) routeAppPrimitive(ctx context.Context, manifest primitive.AppPrimitiveManifest, method string, params json.RawMessage) (primitive.Result, error) {
	ctx, cancel := withDefaultTimeout(ctx, defaultAppPrimitiveTimeout)
	defer cancel()

	start := time.Now()
	result, err := r.callAppSocket(ctx, manifest.SocketPath, method, params)
	if err != nil {
		return primitive.Result{}, err
	}

	// If the manifest declares a verify endpoint, call it now.
	// A failed verify triggers rollback (if declared) and returns an error.
	if manifest.VerifyEndpoint != "" {
		verifyResult, verifyErr := r.callAppSocket(ctx, manifest.SocketPath, manifest.VerifyEndpoint, json.RawMessage("{}"))
		passed := verifyErr == nil && appResultPassed(verifyResult.Data)
		if !passed {
			if manifest.RollbackEndpoint != "" {
				_, _ = r.callAppSocket(ctx, manifest.SocketPath, manifest.RollbackEndpoint, json.RawMessage("{}"))
			}
			if verifyErr != nil {
				return primitive.Result{}, fmt.Errorf("app_primitive_verify_error: %s", verifyErr.Error())
			}
			return primitive.Result{}, errors.New("app_primitive_verify_failed")
		}
	}

	result.Duration = time.Since(start).Milliseconds()
	return result, nil
}

// callAppSocket opens a fresh Unix socket connection, sends one JSON-RPC
// request, and returns the decoded result. One connection per call.
func (r *Router) callAppSocket(ctx context.Context, socketPath, method string, params json.RawMessage) (primitive.Result, error) {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return primitive.Result{}, err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req := appRPCRequest{
		ID:     r.requestID.Add(1),
		Method: method,
		Params: params,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return primitive.Result{}, err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return primitive.Result{}, err
	}

	var resp appRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return primitive.Result{}, err
	}
	if resp.Error != nil {
		return primitive.Result{}, fmt.Errorf("app_primitive_error: %s", resp.Error.Message)
	}

	var data any
	if len(resp.Result) > 0 && string(resp.Result) != "null" {
		if err := json.Unmarshal(resp.Result, &data); err != nil {
			return primitive.Result{}, err
		}
	}

	return primitive.Result{Data: data}, nil
}

// appResultPassed extracts the "passed" boolean from a verify result.
// Returns true if the field is absent (no explicit failure) or true.
func appResultPassed(data any) bool {
	m, ok := data.(map[string]any)
	if !ok {
		return true
	}
	passed, ok := m["passed"].(bool)
	if !ok {
		return true
	}
	return passed
}

type appRPCRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type appRPCResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *appRPCError    `json:"error"`
}

type appRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// RouterDriver delegates runtime operations to named child drivers.
type RouterDriver struct {
	resolver DriverResolver
	drivers  map[string]RuntimeDriver
}

// NewRouterDriver creates a runtime multiplexer.
func NewRouterDriver(resolver DriverResolver, drivers ...RuntimeDriver) *RouterDriver {
	driverMap := make(map[string]RuntimeDriver, len(drivers))
	for _, driver := range drivers {
		if driver != nil {
			driverMap[driver.Name()] = driver
		}
	}
	return &RouterDriver{
		resolver: resolver,
		drivers:  driverMap,
	}
}

func (r *RouterDriver) Name() string { return "router" }

func (r *RouterDriver) Capabilities() []RuntimeCapability {
	var merged []RuntimeCapability
	seen := map[string]bool{}
	for _, driver := range r.drivers {
		for _, capability := range driver.Capabilities() {
			if seen[capability.Name] {
				continue
			}
			seen[capability.Name] = true
			merged = append(merged, capability)
		}
	}
	return merged
}

func (r *RouterDriver) Create(ctx context.Context, config SandboxConfig) (*Sandbox, error) {
	driverName := config.Driver
	if driverName == "" {
		driverName = "docker"
	}
	driver, ok := r.drivers[driverName]
	if !ok {
		return nil, fmt.Errorf("unknown runtime driver: %s", driverName)
	}
	sb, err := driver.Create(ctx, config)
	if err != nil {
		return nil, err
	}
	sb.Driver = driverName
	return sb, nil
}

func (r *RouterDriver) Start(ctx context.Context, sandboxID string) error {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return err
	}
	return driver.Start(ctx, sandboxID)
}

func (r *RouterDriver) Stop(ctx context.Context, sandboxID string) error {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return err
	}
	return driver.Stop(ctx, sandboxID)
}

func (r *RouterDriver) Destroy(ctx context.Context, sandboxID string) error {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return err
	}
	return driver.Destroy(ctx, sandboxID)
}

func (r *RouterDriver) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return nil, err
	}
	return driver.Exec(ctx, sandboxID, cmd)
}

func (r *RouterDriver) Inspect(ctx context.Context, sandboxID string) (*Sandbox, error) {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return nil, err
	}
	return driver.Inspect(ctx, sandboxID)
}

func (r *RouterDriver) Status(ctx context.Context, sandboxID string) (SandboxStatus, error) {
	driver, err := r.resolveDriver(sandboxID)
	if err != nil {
		return StatusError, err
	}
	return driver.Status(ctx, sandboxID)
}

func (r *RouterDriver) resolveDriver(sandboxID string) (RuntimeDriver, error) {
	if r.resolver == nil {
		return nil, fmt.Errorf("runtime resolver unavailable")
	}
	driverName, ok := r.resolver(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}
	driver, ok := r.drivers[driverName]
	if !ok {
		return nil, fmt.Errorf("runtime driver unavailable: %s", driverName)
	}
	return driver, nil
}
