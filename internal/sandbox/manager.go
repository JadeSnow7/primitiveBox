// Package sandbox provides the SandboxManager for creating and managing
// isolated development environments.
package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"primitivebox/internal/eventing"

	"github.com/google/uuid"
)

const (
	defaultRegistryDirName = ".primitivebox/sandboxes"
	defaultRPCHealthPath   = "/health"
)

// Manager manages the lifecycle of sandboxes using a pluggable RuntimeDriver.
type Manager struct {
	driver      RuntimeDriver
	store       Store
	eventBus    *eventing.Bus
	registryDir string
	httpClient  *http.Client

	mu        sync.Mutex
	snapshots map[string]*SnapshotManager
}

// ManagerOptions configures control-plane persistence and background workers.
type ManagerOptions struct {
	Store       Store
	EventBus    *eventing.Bus
	RegistryDir string
}

type legacyRegistryImporter interface {
	ImportLegacyRegistryDir(ctx context.Context, registryDir string) (int, error)
}

// NewManager creates a new SandboxManager with the given runtime driver.
func NewManager(driver RuntimeDriver) *Manager {
	return NewManagerWithOptions(driver, ManagerOptions{
		Store:       NewMemoryStore(),
		RegistryDir: defaultRegistryDir(),
	})
}

// NewManagerWithOptions creates a manager backed by the given store/event bus.
func NewManagerWithOptions(driver RuntimeDriver, options ManagerOptions) *Manager {
	if options.RegistryDir == "" {
		options.RegistryDir = defaultRegistryDir()
	}
	if options.Store == nil {
		options.Store = NewMemoryStore()
	}

	mgr := &Manager{
		driver:      driver,
		store:       options.Store,
		eventBus:    options.EventBus,
		registryDir: options.RegistryDir,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		snapshots: make(map[string]*SnapshotManager),
	}
	_ = mgr.importLegacyRegistry(context.Background())
	return mgr
}

func defaultRegistryDir() string {
	dir := os.Getenv("PB_SANDBOX_REGISTRY_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, defaultRegistryDirName)
	}
	return dir
}

// NewManagerWithRegistryDir creates a manager backed by the given registry directory.
func NewManagerWithRegistryDir(driver RuntimeDriver, registryDir string) *Manager {
	return NewManagerWithOptions(driver, ManagerOptions{
		Store:       NewMemoryStore(),
		RegistryDir: registryDir,
	})
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
	now := time.Now().UTC()
	sandbox.Config = config
	if sandbox.Driver == "" {
		sandbox.Driver = config.Driver
	}
	sandbox.Namespace = config.Namespace
	if sandbox.Capabilities == nil {
		sandbox.Capabilities = cloneCapabilities(m.driver.Capabilities())
	}
	if sandbox.CreatedAt == 0 {
		sandbox.CreatedAt = now.Unix()
	}
	sandbox.UpdatedAt = now.Unix()
	sandbox.LastAccessedAt = now.Unix()
	sandbox.ExpiresAt = computeExpiry(now, sandbox.CreatedAt, sandbox.LastAccessedAt, sandbox.Config.Lifecycle)

	if err := m.upsertSandbox(ctx, sandbox); err != nil {
		return nil, err
	}
	m.publish(ctx, eventing.Event{
		Type:      "sandbox.created",
		Source:    "manager",
		SandboxID: sandbox.ID,
		Message:   "sandbox metadata created",
		Data:      eventing.MustJSON(sandbox),
	})
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
	m.touchLifecycle(refreshed, time.Now().UTC())
	if err := m.upsertSandbox(ctx, refreshed); err != nil {
		return err
	}
	m.publish(ctx, eventing.Event{
		Type:      "sandbox.started",
		Source:    "manager",
		SandboxID: sandboxID,
		Message:   "sandbox started",
	})
	return nil
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
	sb.UpdatedAt = time.Now().UTC().Unix()
	if err := m.upsertSandbox(ctx, sb); err != nil {
		return err
	}
	m.publish(ctx, eventing.Event{
		Type:      "sandbox.stopped",
		Source:    "manager",
		SandboxID: sandboxID,
		Message:   "sandbox stopped",
	})
	return nil
}

// Destroy permanently removes a sandbox.
func (m *Manager) Destroy(ctx context.Context, sandboxID string) error {
	if err := m.driver.Destroy(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to destroy sandbox: %w", err)
	}

	m.mu.Lock()
	delete(m.snapshots, sandboxID)
	m.mu.Unlock()

	if err := m.store.Delete(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to delete sandbox metadata: %w", err)
	}
	m.publish(ctx, eventing.Event{
		Type:      "sandbox.destroyed",
		Source:    "manager",
		SandboxID: sandboxID,
		Message:   "sandbox destroyed",
	})
	return nil
}

// Get retrieves sandbox info by ID.
func (m *Manager) Get(sandboxID string) (*Sandbox, bool) {
	sb, ok, err := m.store.Get(context.Background(), sandboxID)
	if err != nil || !ok {
		return nil, false
	}
	return sb, true
}

// List returns all sandboxes.
func (m *Manager) List(ctx context.Context) ([]*Sandbox, error) {
	items, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]*Sandbox, 0, len(items))
	for _, sb := range items {
		refreshed, err := m.refreshSandboxStatus(ctx, sb)
		if err == nil {
			sb = refreshed
			_ = m.upsertSandbox(ctx, sb)
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
		_ = m.upsertSandbox(ctx, sb)
	}
	return sb, nil
}

// Exec runs a command in a sandbox.
func (m *Manager) Exec(ctx context.Context, sandboxID string, cmd ExecCommand) (*ExecResult, error) {
	result, err := m.driver.Exec(ctx, sandboxID, cmd)
	if err != nil {
		return nil, err
	}
	_ = m.Touch(ctx, sandboxID)
	return result, nil
}

// CreatePlaceholder persists an existing sandbox record.
// Used by tests and gateway bootstrap flows.
func (m *Manager) CreatePlaceholder(sb *Sandbox) error {
	return m.upsertSandbox(context.Background(), sb)
}

// Touch refreshes idle-TTL accounting after a sandbox interaction.
func (m *Manager) Touch(ctx context.Context, sandboxID string) error {
	sb, ok := m.Get(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}
	m.touchLifecycle(sb, time.Now().UTC())
	return m.upsertSandbox(ctx, sb)
}

// ReapExpired destroys sandboxes whose TTL already elapsed.
func (m *Manager) ReapExpired(ctx context.Context, limit int) (int, error) {
	items, err := m.store.ListExpired(ctx, time.Now().UTC(), limit)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, sb := range items {
		if err := m.Destroy(ctx, sb.ID); err != nil {
			return reaped, err
		}
		reaped++
		m.publish(ctx, eventing.Event{
			Type:      "sandbox.reaped",
			Source:    "reaper",
			SandboxID: sb.ID,
			Message:   "sandbox destroyed by TTL reaper",
		})
	}
	return reaped, nil
}

// RunReaper starts a background TTL cleanup loop until the context is cancelled.
func (m *Manager) RunReaper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.ReapExpired(ctx, 32)
		}
	}
}

func (m *Manager) upsertSandbox(ctx context.Context, sb *Sandbox) error {
	if sb.Driver == "" {
		sb.Driver = sb.Config.Driver
	}
	if sb.Capabilities == nil {
		sb.Capabilities = cloneCapabilities(m.driver.Capabilities())
	}
	if sb.UpdatedAt == 0 {
		sb.UpdatedAt = time.Now().UTC().Unix()
	}
	return m.store.Upsert(ctx, sb)
}

func (m *Manager) refreshSandboxStatus(ctx context.Context, sb *Sandbox) (*Sandbox, error) {
	updated := cloneSandbox(sb)
	if updated.Driver == "" {
		updated.Driver = updated.Config.Driver
	}
	if updated.Capabilities == nil {
		updated.Capabilities = cloneCapabilities(m.driver.Capabilities())
	}
	updated.UpdatedAt = time.Now().UTC().Unix()

	inspected, err := m.driver.Inspect(ctx, sb.ID)
	if err != nil {
		status, statusErr := m.driver.Status(ctx, sb.ID)
		if statusErr != nil {
			return sb, statusErr
		}
		updated.Status = status
	} else {
		mergeSandboxState(updated, inspected)
	}

	switch {
	case updated.Status == StatusRunning && m.isHealthy(updated):
		updated.HealthStatus = "healthy"
	case updated.Status == StatusRunning:
		updated.HealthStatus = "starting"
	case updated.Status == StatusStopped:
		updated.HealthStatus = "stopped"
	case updated.Status == StatusDestroyed:
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
	if config.Driver == "" {
		config.Driver = "docker"
	}
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
	if config.Namespace == "" {
		config.Namespace = "default"
	}
	if config.MountSource != "" {
		if abs, err := filepath.Abs(config.MountSource); err == nil {
			config.MountSource = abs
		}
	}
	if len(config.AllowedHosts) > 0 && len(config.NetworkPolicy.AllowHosts) == 0 {
		config.NetworkPolicy.AllowHosts = append([]string(nil), config.AllowedHosts...)
	}
	if config.NetworkPolicy.Mode == NetworkModeUnset {
		switch {
		case !config.NetworkEnabled && len(config.NetworkPolicy.AllowHosts) == 0 && len(config.NetworkPolicy.AllowCIDRs) == 0:
			config.NetworkPolicy.Mode = NetworkModeNone
		case len(config.NetworkPolicy.AllowHosts) > 0 || len(config.NetworkPolicy.AllowCIDRs) > 0 || len(config.NetworkPolicy.AllowPorts) > 0:
			config.NetworkPolicy.Mode = NetworkModePolicy
		default:
			config.NetworkPolicy.Mode = NetworkModeFull
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
	if sb.Config.NetworkPolicy.AllowHosts != nil {
		clone.Config.NetworkPolicy.AllowHosts = append([]string(nil), sb.Config.NetworkPolicy.AllowHosts...)
	}
	if sb.Config.NetworkPolicy.AllowCIDRs != nil {
		clone.Config.NetworkPolicy.AllowCIDRs = append([]string(nil), sb.Config.NetworkPolicy.AllowCIDRs...)
	}
	if sb.Config.NetworkPolicy.AllowPorts != nil {
		clone.Config.NetworkPolicy.AllowPorts = append([]int(nil), sb.Config.NetworkPolicy.AllowPorts...)
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
	if sb.Capabilities != nil {
		clone.Capabilities = cloneCapabilities(sb.Capabilities)
	}
	if sb.Metadata != nil {
		clone.Metadata = make(map[string]string, len(sb.Metadata))
		for k, v := range sb.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}

func cloneCapabilities(capabilities []RuntimeCapability) []RuntimeCapability {
	if capabilities == nil {
		return nil
	}
	out := make([]RuntimeCapability, len(capabilities))
	copy(out, capabilities)
	return out
}

func mergeSandboxState(dst *Sandbox, src *Sandbox) {
	if src == nil {
		return
	}
	if src.ContainerID != "" {
		dst.ContainerID = src.ContainerID
	}
	if src.Driver != "" {
		dst.Driver = src.Driver
	}
	if src.Namespace != "" {
		dst.Namespace = src.Namespace
	}
	if src.Config.Image != "" || src.Config.MountSource != "" {
		dst.Config = src.Config
	}
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.RPCEndpoint != "" {
		dst.RPCEndpoint = src.RPCEndpoint
	}
	if src.RPCPort != 0 {
		dst.RPCPort = src.RPCPort
	}
	if src.CreatedAt != 0 {
		dst.CreatedAt = src.CreatedAt
	}
	if src.UpdatedAt != 0 {
		dst.UpdatedAt = src.UpdatedAt
	}
	if src.LastAccessedAt != 0 {
		dst.LastAccessedAt = src.LastAccessedAt
	}
	if src.ExpiresAt != 0 {
		dst.ExpiresAt = src.ExpiresAt
	}
	if len(src.Labels) > 0 {
		dst.Labels = cloneSandbox(src).Labels
	}
	if len(src.Capabilities) > 0 {
		dst.Capabilities = cloneCapabilities(src.Capabilities)
	}
	if len(src.Metadata) > 0 {
		dst.Metadata = cloneSandbox(src).Metadata
	}
}

func computeExpiry(now time.Time, createdAtUnix, lastAccessedAtUnix int64, policy LifecyclePolicy) int64 {
	if policy.TTLSeconds <= 0 && policy.IdleTTLSeconds <= 0 {
		return 0
	}

	var candidates []time.Time
	if policy.TTLSeconds > 0 {
		candidates = append(candidates, time.Unix(createdAtUnix, 0).Add(time.Duration(policy.TTLSeconds)*time.Second))
	}
	if policy.IdleTTLSeconds > 0 {
		base := now
		if lastAccessedAtUnix > 0 {
			base = time.Unix(lastAccessedAtUnix, 0)
		}
		candidates = append(candidates, base.Add(time.Duration(policy.IdleTTLSeconds)*time.Second))
	}
	if len(candidates) == 0 {
		return 0
	}

	min := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Before(min) {
			min = candidate
		}
	}
	return min.Unix()
}

func (m *Manager) touchLifecycle(sb *Sandbox, now time.Time) {
	if sb == nil {
		return
	}
	sb.LastAccessedAt = now.Unix()
	sb.UpdatedAt = now.Unix()
	sb.ExpiresAt = computeExpiry(now, sb.CreatedAt, sb.LastAccessedAt, sb.Config.Lifecycle)
}

func (m *Manager) importLegacyRegistry(ctx context.Context) error {
	importer, ok := m.store.(legacyRegistryImporter)
	if !ok {
		return nil
	}
	_, err := importer.ImportLegacyRegistryDir(ctx, m.registryDir)
	return err
}

func (m *Manager) publish(ctx context.Context, evt eventing.Event) {
	if m.eventBus != nil {
		m.eventBus.Publish(ctx, evt)
	}
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
