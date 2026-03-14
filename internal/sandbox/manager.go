// Package sandbox provides the SandboxManager for creating and managing
// isolated development environments.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultRegistryDirName = ".primitivebox/sandboxes"
	defaultRPCHealthPath   = "/health"
)

// Manager manages the lifecycle of sandboxes using a pluggable RuntimeDriver.
type Manager struct {
	driver      RuntimeDriver
	registryDir string
	httpClient  *http.Client

	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
	snapshots map[string]*SnapshotManager
}

// NewManager creates a new SandboxManager with the given runtime driver.
func NewManager(driver RuntimeDriver) *Manager {
	dir := os.Getenv("PB_SANDBOX_REGISTRY_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, defaultRegistryDirName)
	}

	return NewManagerWithRegistryDir(driver, dir)
}

// NewManagerWithRegistryDir creates a manager backed by the given registry directory.
func NewManagerWithRegistryDir(driver RuntimeDriver, registryDir string) *Manager {
	mgr := &Manager{
		driver:      driver,
		registryDir: registryDir,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		sandboxes: make(map[string]*Sandbox),
		snapshots: make(map[string]*SnapshotManager),
	}
	_ = mgr.loadRegistry()
	return mgr
}

// RegistryDir returns the on-disk registry directory.
func (m *Manager) RegistryDir() string {
	return m.registryDir
}

// Create provisions a new sandbox with the given configuration.
func (m *Manager) Create(ctx context.Context, config SandboxConfig) (*Sandbox, error) {
	config = applySandboxDefaults(config)

	sandbox, err := m.driver.Create(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}
	sandbox.Config = config

	if err := m.upsertSandbox(sandbox); err != nil {
		return nil, err
	}
	return cloneSandbox(sandbox), nil
}

// Start activates a sandbox.
func (m *Manager) Start(ctx context.Context, sandboxID string) error {
	sb, ok := m.Get(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	if err := m.driver.Start(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to start sandbox: %w", err)
	}

	refreshed, err := m.refreshSandboxStatus(ctx, sb)
	if err != nil {
		return err
	}

	if refreshed.Status == StatusRunning && m.isHealthy(refreshed) {
		refreshed.HealthStatus = "healthy"
	} else if refreshed.Status == StatusRunning {
		refreshed.HealthStatus = "starting"
	}

	return m.upsertSandbox(refreshed)
}

// Stop gracefully stops a sandbox.
func (m *Manager) Stop(ctx context.Context, sandboxID string) error {
	sb, ok := m.Get(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	if err := m.driver.Stop(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	sb.Status = StatusStopped
	sb.HealthStatus = "stopped"
	return m.upsertSandbox(sb)
}

// Destroy permanently removes a sandbox.
func (m *Manager) Destroy(ctx context.Context, sandboxID string) error {
	if err := m.driver.Destroy(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to destroy sandbox: %w", err)
	}

	m.mu.Lock()
	delete(m.sandboxes, sandboxID)
	delete(m.snapshots, sandboxID)
	m.mu.Unlock()

	if err := os.Remove(m.registryPath(sandboxID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove sandbox registry: %w", err)
	}
	return nil
}

// Get retrieves sandbox info by ID.
func (m *Manager) Get(sandboxID string) (*Sandbox, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sb, ok := m.sandboxes[sandboxID]
	if !ok {
		return nil, false
	}
	return cloneSandbox(sb), true
}

// List returns all sandboxes.
func (m *Manager) List(ctx context.Context) ([]*Sandbox, error) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sandboxes))
	for id := range m.sandboxes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	sort.Strings(ids)
	out := make([]*Sandbox, 0, len(ids))
	for _, id := range ids {
		sb, ok := m.Get(id)
		if !ok {
			continue
		}
		refreshed, err := m.refreshSandboxStatus(ctx, sb)
		if err == nil {
			sb = refreshed
			_ = m.upsertSandbox(sb)
		}
		out = append(out, sb)
	}

	return out, nil
}

// Inspect refreshes and returns a sandbox by ID.
func (m *Manager) Inspect(ctx context.Context, sandboxID string) (*Sandbox, error) {
	sb, ok := m.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	refreshed, err := m.refreshSandboxStatus(ctx, sb)
	if err == nil {
		sb = refreshed
		_ = m.upsertSandbox(sb)
	}
	return sb, nil
}

// Exec runs a command in a sandbox.
func (m *Manager) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	return m.driver.Exec(ctx, sandboxID, cmd)
}

// CreatePlaceholder persists an existing sandbox record.
// Used by tests and gateway bootstrap flows.
func (m *Manager) CreatePlaceholder(sb *Sandbox) error {
	return m.upsertSandbox(sb)
}

func (m *Manager) upsertSandbox(sb *Sandbox) error {
	if err := os.MkdirAll(m.registryDir, 0o755); err != nil {
		return fmt.Errorf("failed to create sandbox registry dir: %w", err)
	}

	data, err := json.MarshalIndent(sb, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode sandbox metadata: %w", err)
	}

	m.mu.Lock()
	m.sandboxes[sb.ID] = cloneSandbox(sb)
	m.mu.Unlock()

	if err := os.WriteFile(m.registryPath(sb.ID), data, 0o644); err != nil {
		return fmt.Errorf("failed to persist sandbox metadata: %w", err)
	}

	return nil
}

func (m *Manager) loadRegistry() error {
	if err := os.MkdirAll(m.registryDir, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(m.registryDir)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(m.registryDir, entry.Name()))
		if err != nil {
			continue
		}

		var sb Sandbox
		if err := json.Unmarshal(data, &sb); err != nil {
			continue
		}
		m.sandboxes[sb.ID] = cloneSandbox(&sb)
	}

	return nil
}

func (m *Manager) registryPath(sandboxID string) string {
	return filepath.Join(m.registryDir, sandboxID+".json")
}

func (m *Manager) refreshSandboxStatus(ctx context.Context, sb *Sandbox) (*Sandbox, error) {
	status, err := m.driver.Status(ctx, sb.ID)
	if err != nil {
		return sb, err
	}

	updated := cloneSandbox(sb)
	updated.Status = status

	switch {
	case status == StatusRunning && m.isHealthy(updated):
		updated.HealthStatus = "healthy"
	case status == StatusRunning:
		updated.HealthStatus = "starting"
	case status == StatusStopped:
		updated.HealthStatus = "stopped"
	case status == StatusDestroyed:
		updated.HealthStatus = "destroyed"
	default:
		updated.HealthStatus = "error"
	}

	return updated, nil
}

func (m *Manager) isHealthy(sb *Sandbox) bool {
	if sb.RPCEndpoint == "" {
		return false
	}

	req, err := http.NewRequest(http.MethodGet, sb.RPCEndpoint+defaultRPCHealthPath, nil)
	if err != nil {
		return false
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func applySandboxDefaults(config SandboxConfig) SandboxConfig {
	if config.MountTarget == "" {
		config.MountTarget = "/workspace"
	}
	if config.User == "" {
		config.User = "1000:1000"
	}
	if config.CPULimit == 0 {
		config.CPULimit = 1.0
	}
	if config.MemoryLimit == 0 {
		config.MemoryLimit = 512
	}
	if config.Labels == nil {
		config.Labels = make(map[string]string)
	}
	if config.MountSource != "" {
		if abs, err := filepath.Abs(config.MountSource); err == nil {
			config.MountSource = abs
		}
	}
	config.Labels["managed-by"] = "primitivebox"
	return config
}

func cloneSandbox(sb *Sandbox) *Sandbox {
	if sb == nil {
		return nil
	}

	clone := *sb
	if sb.Config.Env != nil {
		clone.Config.Env = make(map[string]string, len(sb.Config.Env))
		for k, v := range sb.Config.Env {
			clone.Config.Env[k] = v
		}
	}
	if sb.Config.AllowedHosts != nil {
		clone.Config.AllowedHosts = append([]string(nil), sb.Config.AllowedHosts...)
	}
	if sb.Config.Labels != nil {
		clone.Config.Labels = make(map[string]string, len(sb.Config.Labels))
		for k, v := range sb.Config.Labels {
			clone.Config.Labels[k] = v
		}
	}
	if sb.Labels != nil {
		clone.Labels = make(map[string]string, len(sb.Labels))
		for k, v := range sb.Labels {
			clone.Labels[k] = v
		}
	}
	return &clone
}

// UUID Helper
func generateID() string {
	return "sb-" + uuid.New().String()[:8]
}

// SnapshotManager handles file-system snapshots for a single sandbox.
type SnapshotManager struct {
	SandboxID    string
	WorkspaceDir string
	mu           sync.Mutex
	snapshots    []SnapshotEntry
	maxSnapshots int
}

// SnapshotEntry records metadata about a single snapshot.
type SnapshotEntry struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// NewSnapshotManager creates a snapshot manager for a sandbox workspace.
func NewSnapshotManager(sandboxID, workspaceDir string) *SnapshotManager {
	return &SnapshotManager{
		SandboxID:    sandboxID,
		WorkspaceDir: workspaceDir,
		maxSnapshots: 20,
	}
}
