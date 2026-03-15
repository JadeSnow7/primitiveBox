// Package sandbox defines the RuntimeDriver interface and sandbox types.
// The runtime layer is pluggable: Docker for MVP, Firecracker/gVisor for production.
package sandbox

import (
	"context"
	"time"
)

// --------------------------------------------------------------------------
// RuntimeDriver Interface (Pluggable Container Runtime)
// --------------------------------------------------------------------------

// RuntimeDriver abstracts the container runtime. Different implementations
// (Docker, containerd, Firecracker, gVisor) can be swapped without changing
// the sandbox management logic.
type RuntimeDriver interface {
	// Create provisions a new sandbox environment.
	Create(ctx context.Context, config SandboxConfig) (*Sandbox, error)

	// Start activates a stopped sandbox.
	Start(ctx context.Context, sandboxID string) error

	// Stop gracefully stops a running sandbox.
	Stop(ctx context.Context, sandboxID string) error

	// Destroy permanently removes a sandbox and its resources.
	Destroy(ctx context.Context, sandboxID string) error

	// Exec runs a command inside the sandbox.
	Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error)

	// Inspect returns the latest runtime view of a sandbox.
	Inspect(ctx context.Context, sandboxID string) (*Sandbox, error)

	// Status returns the current status of a sandbox.
	Status(ctx context.Context, sandboxID string) (SandboxStatus, error)

	// Capabilities describes what the runtime can support.
	Capabilities() []RuntimeCapability

	// Name returns the runtime driver name (e.g., "docker", "firecracker").
	Name() string
}

// Store persists sandbox metadata for the control plane.
type Store interface {
	Upsert(ctx context.Context, sb *Sandbox) error
	Get(ctx context.Context, sandboxID string) (*Sandbox, bool, error)
	List(ctx context.Context) ([]*Sandbox, error)
	Delete(ctx context.Context, sandboxID string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*Sandbox, error)
}

// --------------------------------------------------------------------------
// Sandbox Types
// --------------------------------------------------------------------------

// SandboxConfig defines the parameters for creating a new sandbox.
type SandboxConfig struct {
	// Driver selects the runtime implementation. Defaults to the manager's driver.
	Driver string `json:"driver,omitempty" yaml:"driver,omitempty"`

	// Image is the container/VM image to use (e.g., "python:3.11-slim").
	Image string `json:"image" yaml:"image"`

	// MountSource is the host directory to mount into the sandbox.
	MountSource string `json:"mount_source" yaml:"mount_source"`

	// MountTarget is the path inside the sandbox (default: /workspace).
	MountTarget string `json:"mount_target" yaml:"mount_target"`

	// Resource limits
	CPULimit    float64 `json:"cpu_limit" yaml:"cpu_limit"`       // CPU cores (e.g., 1.0)
	MemoryLimit int64   `json:"memory_limit" yaml:"memory_limit"` // Memory in MB
	DiskLimit   int64   `json:"disk_limit" yaml:"disk_limit"`     // Disk in MB

	// Network controls
	NetworkEnabled bool          `json:"network_enabled" yaml:"network_enabled"` // Default: false
	AllowedHosts   []string      `json:"allowed_hosts" yaml:"allowed_hosts"`     // Whitelist
	NetworkPolicy  NetworkPolicy `json:"network_policy" yaml:"network_policy"`

	// Optional namespace for runtimes that support logical tenancy.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`

	// Lifecycle controls such as absolute TTL and idle timeout.
	Lifecycle LifecyclePolicy `json:"lifecycle" yaml:"lifecycle"`

	// Security
	User string `json:"user" yaml:"user"` // Run as user (default: "1000:1000")

	// Environment variables injected into the sandbox
	Env map[string]string `json:"env" yaml:"env"`

	// Labels for identification and filtering
	Labels map[string]string `json:"labels" yaml:"labels"`
}

// Sandbox represents a running sandbox instance.
type Sandbox struct {
	ID             string              `json:"id"`
	ContainerID    string              `json:"container_id,omitempty"`
	Driver         string              `json:"driver,omitempty"`
	Namespace      string              `json:"namespace,omitempty"`
	Config         SandboxConfig       `json:"config"`
	Status         SandboxStatus       `json:"status"`
	HealthStatus   string              `json:"health_status,omitempty"`
	RPCEndpoint    string              `json:"rpc_endpoint"` // Unix socket or HTTP addr
	RPCPort        int                 `json:"rpc_port,omitempty"`
	CreatedAt      int64               `json:"created_at"`
	UpdatedAt      int64               `json:"updated_at,omitempty"`
	LastAccessedAt int64               `json:"last_accessed_at,omitempty"`
	ExpiresAt      int64               `json:"expires_at,omitempty"`
	Labels         map[string]string   `json:"labels"`
	Capabilities   []RuntimeCapability `json:"capabilities,omitempty"`
	Metadata       map[string]string   `json:"metadata,omitempty"`
}

// SandboxStatus represents the lifecycle state of a sandbox.
type SandboxStatus string

const (
	StatusCreating  SandboxStatus = "creating"
	StatusRunning   SandboxStatus = "running"
	StatusStopped   SandboxStatus = "stopped"
	StatusError     SandboxStatus = "error"
	StatusDestroyed SandboxStatus = "destroyed"
)

// RuntimeCapability describes a runtime-level feature toggle.
type RuntimeCapability struct {
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Notes     string `json:"notes,omitempty"`
}

// LifecyclePolicy controls automatic sandbox expiration.
type LifecyclePolicy struct {
	TTLSeconds     int64 `json:"ttl_seconds,omitempty" yaml:"ttl_seconds,omitempty"`
	IdleTTLSeconds int64 `json:"idle_ttl_seconds,omitempty" yaml:"idle_ttl_seconds,omitempty"`
}

// NetworkPolicy describes egress isolation behavior.
type NetworkPolicy struct {
	Mode       NetworkMode `json:"mode,omitempty" yaml:"mode,omitempty"`
	AllowHosts []string    `json:"allow_hosts,omitempty" yaml:"allow_hosts,omitempty"`
	AllowCIDRs []string    `json:"allow_cidrs,omitempty" yaml:"allow_cidrs,omitempty"`
	AllowPorts []int       `json:"allow_ports,omitempty" yaml:"allow_ports,omitempty"`
}

// NetworkMode defines coarse runtime network behavior.
type NetworkMode string

const (
	NetworkModeUnset  NetworkMode = ""
	NetworkModeNone   NetworkMode = "none"
	NetworkModeFull   NetworkMode = "full"
	NetworkModePolicy NetworkMode = "policy"
)

// --------------------------------------------------------------------------
// Exec Types
// --------------------------------------------------------------------------

// ExecCommand defines a command to run inside a sandbox.
type ExecCommand struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Timeout    int               `json:"timeout_s,omitempty"` // 0 = use default
	User       string            `json:"user,omitempty"`      // Override user
}

// ExecResult captures the output of a command execution.
type ExecResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}
