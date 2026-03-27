package pkgmgr

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a package is not found in the registry.
var ErrNotFound = errors.New("package not found")

// LocalRegistry is a local package registry with builtin packages.
type LocalRegistry struct{}

// NewLocalRegistry creates a new LocalRegistry.
func NewLocalRegistry() *LocalRegistry { return &LocalRegistry{} }

// Lookup returns a PackageManifest by name or ErrNotFound.
func (r *LocalRegistry) Lookup(name string) (PackageManifest, error) {
	pkg, ok := builtinPackages[name]
	if !ok {
		return PackageManifest{}, ErrNotFound
	}
	return pkg, nil
}

// List returns all registered packages.
func (r *LocalRegistry) List() []PackageManifest {
	result := make([]PackageManifest, 0, len(builtinPackages))
	for _, pkg := range builtinPackages {
		result = append(result, pkg)
	}
	return result
}

var builtinPackages = map[string]PackageManifest{
	"os": {
		Name:        "os",
		Version:     "0.1.0",
		Description: "OS adapter: process.*, service.*, pkg.* primitives",
		Adapter: AdapterConfig{
			Type:       "binary",
			BinaryPath: "{pb_dir}/pb-os-adapter",
			SocketPath: "{workspace}/.pb/sockets/os-adapter.sock",
		},
		Healthcheck: &HealthcheckSpec{
			Primitive: "process.list",
			Params:    map[string]any{},
			Timeout:   5 * time.Second,
		},
		Primitives: []PrimitiveSpec{
			{Name: "process.list", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
			{Name: "process.spawn", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: false}},
			{Name: "process.terminate", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: false}},
			{Name: "process.kill", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
			{Name: "service.status", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
			{Name: "service.start", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: true}},
			{Name: "service.stop", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "medium", Reversible: true}},
			{Name: "pkg.list", Intent: PrimitiveIntent{Category: "query", SideEffect: "read", RiskLevel: "low", Reversible: true}},
			{Name: "pkg.install", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
			{Name: "pkg.remove", Intent: PrimitiveIntent{Category: "mutation", SideEffect: "exec", RiskLevel: "high", Reversible: false}},
			{Name: "pkg.verify", Intent: PrimitiveIntent{Category: "verification", SideEffect: "read", RiskLevel: "low", Reversible: true}},
		},
	},
	"mcp-bridge": {
		Name:        "mcp-bridge",
		Version:     "0.1.0",
		Description: "MCP bridge: mirrors any MCP stdio server as PrimitiveBox primitives",
		Adapter: AdapterConfig{
			Type:       "binary",
			BinaryPath: "{pb_dir}/pb-mcp-bridge",
			SocketPath: "{workspace}/.pb/sockets/mcp-bridge.sock",
		},
		Primitives: nil,
	},
}
