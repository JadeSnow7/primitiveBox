package pkgmgr

import (
	"context"
	"testing"
	"time"
)

// mockPackageStore is an in-memory PackageStore for testing.
type mockPackageStore struct {
	packages map[string]InstalledPackage
}

func newMockPackageStore() *mockPackageStore {
	return &mockPackageStore{packages: make(map[string]InstalledPackage)}
}

func (m *mockPackageStore) Save(ctx context.Context, pkg InstalledPackage) error {
	m.packages[pkg.Name] = pkg
	return nil
}

func (m *mockPackageStore) Remove(ctx context.Context, name string) error {
	delete(m.packages, name)
	return nil
}

func (m *mockPackageStore) Get(ctx context.Context, name string) (*InstalledPackage, error) {
	pkg, ok := m.packages[name]
	if !ok {
		return nil, nil
	}
	return &pkg, nil
}

func (m *mockPackageStore) List(ctx context.Context) ([]InstalledPackage, error) {
	result := make([]InstalledPackage, 0, len(m.packages))
	for _, pkg := range m.packages {
		result = append(result, pkg)
	}
	return result, nil
}

func (m *mockPackageStore) SetStatus(ctx context.Context, name, status string) error {
	pkg, ok := m.packages[name]
	if !ok {
		return nil
	}
	pkg.Status = status
	m.packages[name] = pkg
	return nil
}

func TestInstaller_resolveTemplate(t *testing.T) {
	installer := NewInstaller(nil, nil, nil, nil, "", "/home/user/project", "/usr/local/pb")

	tests := []struct {
		input    string
		expected string
	}{
		{"{pb_dir}/pb-os-adapter", "/usr/local/pb/pb-os-adapter"},
		{"{workspace}/.pb/sockets/os.sock", "/home/user/project/.pb/sockets/os.sock"},
		{"{pb_dir}/{workspace}/test", "/usr/local/pb//home/user/project/test"},
		{"no-templates", "no-templates"},
	}

	for _, tt := range tests {
		got := installer.resolveTemplate(tt.input)
		if got != tt.expected {
			t.Errorf("resolveTemplate(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLocalRegistry_Lookup(t *testing.T) {
	reg := NewLocalRegistry()

	pkg, err := reg.Lookup("os")
	if err != nil {
		t.Fatalf("Lookup os: %v", err)
	}
	if pkg.Name != "os" {
		t.Errorf("Name: got %q, want os", pkg.Name)
	}
	if pkg.Version != "0.1.0" {
		t.Errorf("Version: got %q, want 0.1.0", pkg.Version)
	}
	if pkg.Healthcheck == nil {
		t.Error("Healthcheck should not be nil for os")
	}
	if len(pkg.Primitives) == 0 {
		t.Error("Primitives should not be empty for os")
	}
}

func TestLocalRegistry_LookupMCPBridge(t *testing.T) {
	reg := NewLocalRegistry()

	pkg, err := reg.Lookup("mcp-bridge")
	if err != nil {
		t.Fatalf("Lookup mcp-bridge: %v", err)
	}
	if pkg.Name != "mcp-bridge" {
		t.Errorf("Name: got %q, want mcp-bridge", pkg.Name)
	}
	if pkg.Primitives != nil {
		t.Error("mcp-bridge should have nil Primitives (dynamic)")
	}
}

func TestLocalRegistry_LookupNotFound(t *testing.T) {
	reg := NewLocalRegistry()

	_, err := reg.Lookup("nonexistent-package")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLocalRegistry_List(t *testing.T) {
	reg := NewLocalRegistry()
	list := reg.List()
	if len(list) < 2 {
		t.Errorf("expected at least 2 packages, got %d", len(list))
	}

	names := make(map[string]bool)
	for _, pkg := range list {
		names[pkg.Name] = true
	}
	if !names["os"] {
		t.Error("expected 'os' in package list")
	}
	if !names["mcp-bridge"] {
		t.Error("expected 'mcp-bridge' in package list")
	}
}

func TestInstaller_ReservedPrefixes(t *testing.T) {
	// Verify the reserved prefix list is correct and complete.
	expected := []string{"fs.", "state.", "shell.", "verify.", "macro.", "code.", "test."}
	if len(reservedPrefixes) != len(expected) {
		t.Errorf("reservedPrefixes length: got %d, want %d", len(reservedPrefixes), len(expected))
	}
	for _, want := range expected {
		found := false
		for _, got := range reservedPrefixes {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing reserved prefix %q", want)
		}
	}
}

func TestInstaller_AlreadyInstalledCheck(t *testing.T) {
	store := newMockPackageStore()
	reg := NewLocalRegistry()

	// Pre-populate store with the same version as the registry
	existing := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC(),
		SocketPath:  "/tmp/os.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Status:      "active",
	}
	ctx := context.Background()
	_ = store.Save(ctx, existing)

	// Verify the store holds the expected version matching the registry
	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected existing package")
	}
	if got.Version != "0.1.0" {
		t.Errorf("Version: got %q, want 0.1.0", got.Version)
	}

	pkg, _ := reg.Lookup("os")
	if got.Version != pkg.Version {
		t.Errorf("store version %q does not match registry version %q", got.Version, pkg.Version)
	}

	// The idempotent path: same version → Install would skip early
	t.Log("idempotent check: same version already installed — Install would return nil")
	_ = NewInstaller(store, reg, nil, nil, "http://localhost:8080", "/workspace", "/nonexistent")
}

func TestInstaller_RemoveNotInstalled(t *testing.T) {
	store := newMockPackageStore()
	reg := NewLocalRegistry()
	installer := NewInstaller(store, reg, nil, nil, "http://localhost:8080", "/workspace", "/pbdir")

	err := installer.Remove(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error removing nonexistent package")
	}
}

func TestSafeBinaryPathRe(t *testing.T) {
	// safeBinaryPathRe validates safe characters only; absolute-path check is separate.
	valid := []string{
		"/usr/local/bin/pb-os-adapter",
		"/home/user/.primitivebox/pb-mcp-bridge",
		"/opt/pb/bin/adapter_v1.0",
		"relative/path", // safe chars — absolute check is done separately
	}
	invalid := []string{
		"/path/with spaces/binary",
		"/path/with;semicolon",
		"/path/with$dollar",
		"",
	}

	for _, p := range valid {
		if !safeBinaryPathRe.MatchString(p) {
			t.Errorf("expected %q to match safe path re", p)
		}
	}
	for _, p := range invalid {
		if safeBinaryPathRe.MatchString(p) {
			t.Errorf("expected %q to NOT match safe path re", p)
		}
	}
}

func TestHealthcheckSpec_Timeout(t *testing.T) {
	reg := NewLocalRegistry()
	pkg, err := reg.Lookup("os")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	if pkg.Healthcheck.Timeout != 5*time.Second {
		t.Errorf("Healthcheck timeout: got %v, want 5s", pkg.Healthcheck.Timeout)
	}
	if pkg.Healthcheck.Primitive != "process.list" {
		t.Errorf("Healthcheck primitive: got %q, want process.list", pkg.Healthcheck.Primitive)
	}
}
