package pkgmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
)

var (
	// ErrRegistrationTimeout is returned when an adapter fails to register primitives in time.
	ErrRegistrationTimeout = errors.New("adapter registration timed out")
	// ErrHealthcheckFailed is returned when the adapter healthcheck fails.
	ErrHealthcheckFailed = errors.New("adapter healthcheck failed")
	// ErrReservedNamespace is returned when a package declares primitives in a reserved namespace.
	ErrReservedNamespace = errors.New("package declares primitives in reserved namespace")
	// ErrAlreadyInstalled is returned when a package at the same version is already installed.
	ErrAlreadyInstalled = errors.New("package already installed at same version")
)

// reservedPrefixes are system primitive namespace prefixes that adapters cannot use.
var reservedPrefixes = []string{"fs.", "state.", "shell.", "verify.", "macro.", "code.", "test."}

// safeBinaryPathRe matches absolute paths with only safe characters.
var safeBinaryPathRe = regexp.MustCompile(`^[a-zA-Z0-9/_\-\.]+$`)

// Installer orchestrates the install/remove lifecycle for adapter packages.
type Installer struct {
	store      PackageStore
	registry   *LocalRegistry
	appReg     primitive.AppPrimitiveRegistry
	bus        *eventing.Bus
	gatewayURL string
	workspace  string
	pbDir      string

	mu        sync.Mutex
	processes map[string]*os.Process // name → running process
}

// NewInstaller creates a new Installer. appReg and bus may be nil.
func NewInstaller(
	store PackageStore,
	registry *LocalRegistry,
	appReg primitive.AppPrimitiveRegistry,
	bus *eventing.Bus,
	gatewayURL, workspace, pbDir string,
) *Installer {
	return &Installer{
		store:      store,
		registry:   registry,
		appReg:     appReg,
		bus:        bus,
		gatewayURL: gatewayURL,
		workspace:  workspace,
		pbDir:      pbDir,
		processes:  make(map[string]*os.Process),
	}
}

// Install resolves a package by name from the local registry and installs it.
func (i *Installer) Install(ctx context.Context, name string, extraArgs []string) error {
	pkg, err := i.registry.Lookup(name)
	if err != nil {
		return fmt.Errorf("resolve package %q: %w", name, err)
	}
	return i.InstallFromManifest(ctx, pkg, extraArgs)
}

// InstallFromManifest validates, launches, and persists an adapter described by the
// given PackageManifest. This is the install path for Boxfile-sourced packages where
// the manifest has already been parsed and resolved from disk.
func (i *Installer) InstallFromManifest(ctx context.Context, pkg PackageManifest, extraArgs []string) error {
	binaryPath := i.resolveTemplate(pkg.Adapter.BinaryPath)
	socketPath := i.resolveTemplate(pkg.Adapter.SocketPath)
	resolvedAdapterArgs := make([]string, len(pkg.Adapter.Args))
	for idx, a := range pkg.Adapter.Args {
		resolvedAdapterArgs[idx] = i.resolveTemplate(a)
	}

	// PREFLIGHT: validate binary path safety
	if !filepath.IsAbs(binaryPath) {
		return fmt.Errorf("resolved binary path %q is not absolute", binaryPath)
	}
	if !safeBinaryPathRe.MatchString(binaryPath) {
		return fmt.Errorf("resolved binary path %q contains unsafe characters", binaryPath)
	}

	// PREFLIGHT: reserved namespaces
	for _, spec := range pkg.Primitives {
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(spec.Name, prefix) {
				return fmt.Errorf("%w: %q starts with reserved prefix %q", ErrReservedNamespace, spec.Name, prefix)
			}
		}
	}

	// PREFLIGHT: binary exists
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("binary not found at %q: %w", binaryPath, err)
	}

	// PREFLIGHT: already installed (idempotent)
	existing, err := i.store.Get(ctx, pkg.Name)
	if err != nil {
		return fmt.Errorf("check existing install: %w", err)
	}
	if existing != nil && existing.Version == pkg.Version {
		log.Printf("[pkgmgr] Package %q v%s already installed, skipping", pkg.Name, pkg.Version)
		return nil
	}

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// LAUNCH
	args := []string{"--socket", socketPath}
	if i.gatewayURL != "" {
		args = append(args, "--rpc-endpoint", i.gatewayURL)
	}
	args = append(args, resolvedAdapterArgs...)
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Stderr = &prefixedWriter{prefix: "[pkg:" + pkg.Name + "] ", w: os.Stderr}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start adapter %q: %w", pkg.Name, err)
	}
	proc := cmd.Process

	i.mu.Lock()
	i.processes[pkg.Name] = proc
	i.mu.Unlock()

	// On any failure after launch, kill the process and clean up
	rollback := func(cause error) error {
		_ = proc.Kill()
		i.mu.Lock()
		delete(i.processes, pkg.Name)
		i.mu.Unlock()
		_ = i.store.Remove(ctx, pkg.Name)
		return cause
	}

	// WAIT: poll appReg for primitives (up to 10s), or sleep if appReg is nil
	if i.appReg != nil {
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		registered := false
		for !registered {
			select {
			case <-waitCtx.Done():
				return rollback(ErrRegistrationTimeout)
			case <-time.After(250 * time.Millisecond):
			}

			manifests, err := i.appReg.List(waitCtx)
			if err != nil {
				continue
			}

			if len(pkg.Primitives) == 0 {
				// Dynamic registration (e.g. mcp-bridge): wait for any primitive on this socket
				// or treat as registered after adapter is running (1s grace)
				for _, m := range manifests {
					if m.SocketPath == socketPath {
						registered = true
						break
					}
				}
				if !registered {
					// No declared primitives; consider registered immediately since adapter
					// will self-register. The 250ms tick serves as our grace period.
					registered = true
				}
			} else {
				// Check that at least one declared primitive from this package is registered
				registeredNames := make(map[string]bool, len(manifests))
				for _, m := range manifests {
					registeredNames[m.Name] = true
				}
				for _, spec := range pkg.Primitives {
					if registeredNames[spec.Name] {
						registered = true
						break
					}
				}
			}
		}
	} else {
		// No appReg available (CLI path without live registry access): wait 1s grace
		select {
		case <-ctx.Done():
			return rollback(ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	// VERIFY: healthcheck
	if pkg.Healthcheck != nil {
		hcCtx, hcCancel := context.WithTimeout(ctx, pkg.Healthcheck.Timeout)
		defer hcCancel()

		if err := i.runHealthcheck(hcCtx, pkg.Healthcheck); err != nil {
			return rollback(fmt.Errorf("%w: %v", ErrHealthcheckFailed, err))
		}
	}

	// PERSIST
	installed := InstalledPackage{
		Name:        pkg.Name,
		Version:     pkg.Version,
		InstalledAt: time.Now().UTC(),
		SocketPath:  socketPath,
		BinaryPath:  binaryPath,
		Args:        args,
		Status:      "active",
	}
	if err := i.store.Save(ctx, installed); err != nil {
		return rollback(fmt.Errorf("persist install: %w", err))
	}

	if i.bus != nil {
		i.bus.Publish(ctx, eventing.Event{
			Type:    "package.installed",
			Source:  "pkgmgr",
			Message: pkg.Name,
			Data:    eventing.MustJSON(map[string]any{"name": pkg.Name, "version": pkg.Version}),
		})
	}

	log.Printf("[pkgmgr] Package %q v%s installed (pid %d)", pkg.Name, pkg.Version, proc.Pid)
	return nil
}

// Remove drains, signals, and removes a package.
func (i *Installer) Remove(ctx context.Context, name string) error {
	// LOOKUP
	existing, err := i.store.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("lookup installed package: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", ErrNotInstalled, name)
	}

	// DRAIN: mark primitives unavailable
	if i.appReg != nil {
		if err := i.appReg.MarkUnavailable(ctx, name); err != nil {
			log.Printf("[pkgmgr] Warning: mark unavailable %q: %v", name, err)
		}
	}

	// SIGNAL: find and stop process
	i.mu.Lock()
	proc, hasProc := i.processes[name]
	i.mu.Unlock()

	if hasProc && proc != nil {
		_ = proc.Signal(syscall.SIGTERM)
		// Wait for socket to disappear (up to 5s)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(existing.SocketPath); os.IsNotExist(err) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		// Force kill if still running
		_ = proc.Kill()
		i.mu.Lock()
		delete(i.processes, name)
		i.mu.Unlock()
	}

	// PERSIST
	if err := i.store.Remove(ctx, name); err != nil {
		return fmt.Errorf("remove package record: %w", err)
	}

	if i.bus != nil {
		i.bus.Publish(ctx, eventing.Event{
			Type:    "package.removed",
			Source:  "pkgmgr",
			Message: name,
			Data:    eventing.MustJSON(map[string]any{"name": name}),
		})
	}

	log.Printf("[pkgmgr] Package %q removed", name)
	return nil
}

// LaunchInstalled re-launches all installed packages (called on server start).
func (i *Installer) LaunchInstalled(ctx context.Context) error {
	pkgs, err := i.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list installed packages: %w", err)
	}
	for _, pkg := range pkgs {
		if pkg.Status != "installed" && pkg.Status != "active" {
			continue
		}
		if err := i.launchFromRecord(ctx, pkg); err != nil {
			log.Printf("[pkgmgr] Warning: failed to launch %q: %v", pkg.Name, err)
		}
	}
	return nil
}

// launchFromRecord starts a previously-installed package adapter without re-running preflight/verify/persist.
func (i *Installer) launchFromRecord(ctx context.Context, pkg InstalledPackage) error {
	if !filepath.IsAbs(pkg.BinaryPath) {
		return fmt.Errorf("stored binary path %q is not absolute", pkg.BinaryPath)
	}
	if !safeBinaryPathRe.MatchString(pkg.BinaryPath) {
		return fmt.Errorf("stored binary path %q contains unsafe characters", pkg.BinaryPath)
	}
	if _, err := os.Stat(pkg.BinaryPath); err != nil {
		return fmt.Errorf("binary not found at %q: %w", pkg.BinaryPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	var args []string
	if len(pkg.Args) > 0 {
		args = pkg.Args
	} else {
		args = []string{"--socket", pkg.SocketPath}
	}

	cmd := exec.CommandContext(ctx, pkg.BinaryPath, args...)
	cmd.Stderr = &prefixedWriter{prefix: "[pkg:" + pkg.Name + "] ", w: os.Stderr}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start adapter %q: %w", pkg.Name, err)
	}

	i.mu.Lock()
	i.processes[pkg.Name] = cmd.Process
	i.mu.Unlock()

	if err := i.store.SetStatus(ctx, pkg.Name, "active"); err != nil {
		log.Printf("[pkgmgr] Warning: update status for %q: %v", pkg.Name, err)
	}

	log.Printf("[pkgmgr] Re-launched %q (pid %d)", pkg.Name, cmd.Process.Pid)
	return nil
}

func (i *Installer) resolveTemplate(s string) string {
	s = strings.ReplaceAll(s, "{pb_dir}", i.pbDir)
	s = strings.ReplaceAll(s, "{workspace}", i.workspace)
	return s
}

func (i *Installer) runHealthcheck(ctx context.Context, hc *HealthcheckSpec) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  hc.Primitive,
		"params":  hc.Params,
		"id":      1,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.gatewayURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var rpcResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("decode healthcheck response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("healthcheck RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return nil
}

// prefixedWriter prepends a prefix to each line written.
type prefixedWriter struct {
	prefix string
	w      io.Writer
}

func (pw *prefixedWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line != "" {
			fmt.Fprintf(pw.w, "%s%s\n", pw.prefix, line)
		}
	}
	return len(p), nil
}
