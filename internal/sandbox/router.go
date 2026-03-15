package sandbox

import (
	"context"
	"fmt"
)

// DriverResolver maps a sandbox ID back to the runtime name that owns it.
type DriverResolver func(sandboxID string) (string, bool)

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
