package pkgmgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ─── ParseBoxfile ──────────────────────────────────────────────────────────────

func TestParseBoxfile_Valid(t *testing.T) {
	t.Parallel()

	yaml := `
name: my-data-pack
version: 1.0.0
description: Data Science workspace
adapter:
  type: binary
  binary_path: ./adapters/data-adapter
  socket_path: .pb/sockets/data.sock
primitives:
  - name: data.query
    description: Run SQL
    intent:
      category: query
      side_effect: read
      risk_level: low
      reversible: true
healthcheck:
  primitive: data.query
  timeout: 5s
`
	bf, err := ParseBoxfile([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseBoxfile: %v", err)
	}
	if bf.Name != "my-data-pack" {
		t.Errorf("Name: got %q, want my-data-pack", bf.Name)
	}
	if bf.Version != "1.0.0" {
		t.Errorf("Version: got %q, want 1.0.0", bf.Version)
	}
	if bf.Adapter.Type != "binary" {
		t.Errorf("Adapter.Type: got %q, want binary", bf.Adapter.Type)
	}
	if len(bf.Primitives) != 1 {
		t.Fatalf("Primitives: got %d, want 1", len(bf.Primitives))
	}
	if bf.Primitives[0].Name != "data.query" {
		t.Errorf("Primitive[0].Name: got %q, want data.query", bf.Primitives[0].Name)
	}
	if bf.Primitives[0].Intent.RiskLevel != "low" {
		t.Errorf("Primitive[0].Intent.RiskLevel: got %q, want low", bf.Primitives[0].Intent.RiskLevel)
	}
	if bf.Healthcheck == nil {
		t.Fatal("Healthcheck: expected non-nil")
	}
	if bf.Healthcheck.Primitive != "data.query" {
		t.Errorf("Healthcheck.Primitive: got %q, want data.query", bf.Healthcheck.Primitive)
	}
}

func TestParseBoxfile_MalformedYAML(t *testing.T) {
	t.Parallel()

	_, err := ParseBoxfile([]byte("name: [unterminated"))
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
	if !errors.Is(err, ErrBoxfileInvalid) {
		t.Errorf("expected ErrBoxfileInvalid, got %v", err)
	}
}

func TestParseBoxfile_UnknownFields(t *testing.T) {
	t.Parallel()

	yaml := `
name: test-pack
version: 0.1.0
adapter:
  type: binary
  binary_path: ./bin/adapter
  socket_path: .pb/adapter.sock
unknown_field: this should not be here
`
	_, err := ParseBoxfile([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !errors.Is(err, ErrBoxfileInvalid) {
		t.Errorf("expected ErrBoxfileInvalid, got %v", err)
	}
}

// ─── ValidateBoxfile ───────────────────────────────────────────────────────────

func TestValidateBoxfile_MissingName(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Version: "1.0.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateBoxfile_InvalidName(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "My_PACK", // uppercase not allowed
		Version: "1.0.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
	}
	err := ValidateBoxfile(bf)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !errors.Is(err, ErrBoxfileInvalid) {
		t.Errorf("expected ErrBoxfileInvalid, got %v", err)
	}
}

func TestValidateBoxfile_MissingVersion(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestValidateBoxfile_InvalidAdapterType(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "1.0.0",
		Adapter: BoxfileAdapter{Type: "docker", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
	}
	err := ValidateBoxfile(bf)
	if err == nil {
		t.Fatal("expected error for invalid adapter type")
	}
	if !errors.Is(err, ErrBoxfileInvalid) {
		t.Errorf("expected ErrBoxfileInvalid, got %v", err)
	}
}

func TestValidateBoxfile_MissingSocketPath(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "1.0.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x"},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for missing socket_path")
	}
}

func TestValidateBoxfile_ReservedNamespace(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "bad-pack",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
		Primitives: []BoxfilePrimitive{
			{Name: "fs.read", Intent: BoxfileIntent{Category: "query"}},
		},
	}
	err := ValidateBoxfile(bf)
	if err == nil {
		t.Fatal("expected error for reserved namespace primitive")
	}
	if !errors.Is(err, ErrReservedNamespace) {
		t.Errorf("expected ErrReservedNamespace, got %v", err)
	}
}

func TestValidateBoxfile_BootstrapPathTraversal(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
		Bootstrap: &BoxfileBootstrap{
			Files: []BoxfileBootstrapFile{
				{Src: "config.env", Dst: "../../etc/passwd"},
			},
		},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for path traversal in bootstrap dst")
	}
}

func TestValidateBoxfile_BootstrapAbsoluteDst(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
		Bootstrap: &BoxfileBootstrap{
			Files: []BoxfileBootstrapFile{
				{Src: "config.env", Dst: "/etc/config"},
			},
		},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for absolute dst in bootstrap")
	}
}

func TestValidateBoxfile_MCPRequiresCommand(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-mcp-pack",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{
			Type:       "mcp",
			SocketPath: ".pb/mcp.sock",
			// Neither BinaryPath nor MCPCommand set
		},
	}
	if err := ValidateBoxfile(bf); err == nil {
		t.Fatal("expected error for mcp adapter with no command or binary")
	}
}

// ─── BoxfileToManifest ─────────────────────────────────────────────────────────

func TestBoxfileToManifest_BinaryRelativePath(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "1.0.0",
		Adapter: BoxfileAdapter{
			Type:       "binary",
			BinaryPath: "./adapters/my-adapter",
			SocketPath: ".pb/my-adapter.sock",
		},
	}
	baseDir := "/home/user/my-data-pack"
	manifest, err := BoxfileToManifest(bf, baseDir)
	if err != nil {
		t.Fatalf("BoxfileToManifest: %v", err)
	}
	if manifest.Name != "my-pack" {
		t.Errorf("Name: got %q, want my-pack", manifest.Name)
	}
	// Relative path should be resolved to absolute
	expected := "/home/user/my-data-pack/adapters/my-adapter"
	if manifest.Adapter.BinaryPath != expected {
		t.Errorf("BinaryPath: got %q, want %q", manifest.Adapter.BinaryPath, expected)
	}
	// Relative socket path should be prefixed with {workspace}/
	if manifest.Adapter.SocketPath != "{workspace}/.pb/my-adapter.sock" {
		t.Errorf("SocketPath: got %q, want {workspace}/.pb/my-adapter.sock", manifest.Adapter.SocketPath)
	}
}

func TestBoxfileToManifest_MCPWithCommand(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "jupyter-mcp",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{
			Type:       "mcp",
			MCPCommand: "python3 -m jupyter_mcp",
			SocketPath: ".pb/jupyter.sock",
		},
	}
	manifest, err := BoxfileToManifest(bf, "/any/dir")
	if err != nil {
		t.Fatalf("BoxfileToManifest: %v", err)
	}
	// Should fall back to pb-mcp-bridge as the binary
	if manifest.Adapter.BinaryPath != "{pb_dir}/pb-mcp-bridge" {
		t.Errorf("BinaryPath: got %q, want {pb_dir}/pb-mcp-bridge", manifest.Adapter.BinaryPath)
	}
	// MCPCommand should be prepended to args
	if len(manifest.Adapter.Args) < 2 || manifest.Adapter.Args[0] != "--cmd" {
		t.Errorf("Args: got %v, want [--cmd, ...]", manifest.Adapter.Args)
	}
	if manifest.Adapter.Args[1] != "python3 -m jupyter_mcp" {
		t.Errorf("Args[1]: got %q, want 'python3 -m jupyter_mcp'", manifest.Adapter.Args[1])
	}
}

func TestBoxfileToManifest_TemplatePathsUnchanged(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "os-adapter",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{
			Type:       "binary",
			BinaryPath: "{pb_dir}/pb-os-adapter",
			SocketPath: "{workspace}/.pb/sockets/os.sock",
		},
	}
	manifest, err := BoxfileToManifest(bf, "/irrelevant")
	if err != nil {
		t.Fatalf("BoxfileToManifest: %v", err)
	}
	if manifest.Adapter.BinaryPath != "{pb_dir}/pb-os-adapter" {
		t.Errorf("BinaryPath: got %q, want {pb_dir}/pb-os-adapter", manifest.Adapter.BinaryPath)
	}
	if manifest.Adapter.SocketPath != "{workspace}/.pb/sockets/os.sock" {
		t.Errorf("SocketPath: got %q, want {workspace}/.pb/sockets/os.sock", manifest.Adapter.SocketPath)
	}
}

func TestBoxfileToManifest_HealthcheckTimeout(t *testing.T) {
	t.Parallel()

	bf := Boxfile{
		Name:    "my-pack",
		Version: "0.1.0",
		Adapter: BoxfileAdapter{Type: "binary", BinaryPath: "./bin/x", SocketPath: ".pb/x.sock"},
		Healthcheck: &BoxfileHealthcheck{
			Primitive: "my.check",
			Timeout:   "10s",
		},
	}
	manifest, err := BoxfileToManifest(bf, "/dir")
	if err != nil {
		t.Fatalf("BoxfileToManifest: %v", err)
	}
	if manifest.Healthcheck == nil {
		t.Fatal("Healthcheck: expected non-nil")
	}
	if manifest.Healthcheck.Timeout.Seconds() != 10 {
		t.Errorf("Healthcheck.Timeout: got %v, want 10s", manifest.Healthcheck.Timeout)
	}
}

// ─── LoadBoxfile ───────────────────────────────────────────────────────────────

func TestLoadBoxfile_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, _, err := LoadBoxfile(dir)
	if err == nil {
		t.Fatal("expected ErrBoxfileNotFound, got nil")
	}
	if !errors.Is(err, ErrBoxfileNotFound) {
		t.Errorf("expected ErrBoxfileNotFound, got %v", err)
	}
}

func TestLoadBoxfile_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := `
name: local-pack
version: 0.2.0
description: A locally-authored package
adapter:
  type: binary
  binary_path: ./bin/local-adapter
  socket_path: .pb/local.sock
`
	if err := os.WriteFile(filepath.Join(dir, "Boxfile"), []byte(content), 0o644); err != nil {
		t.Fatalf("write Boxfile: %v", err)
	}

	bf, path, err := LoadBoxfile(dir)
	if err != nil {
		t.Fatalf("LoadBoxfile: %v", err)
	}
	if bf.Name != "local-pack" {
		t.Errorf("Name: got %q, want local-pack", bf.Name)
	}
	if bf.Version != "0.2.0" {
		t.Errorf("Version: got %q, want 0.2.0", bf.Version)
	}
	if path != filepath.Join(dir, "Boxfile") {
		t.Errorf("path: got %q, want %q", path, filepath.Join(dir, "Boxfile"))
	}
}

func TestLoadBoxfile_YamlExtension(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := `
name: yaml-pack
version: 1.0.0
adapter:
  type: binary
  binary_path: ./bin/adapter
  socket_path: .pb/adapter.sock
`
	if err := os.WriteFile(filepath.Join(dir, "boxfile.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write boxfile.yaml: %v", err)
	}

	bf, _, err := LoadBoxfile(dir)
	if err != nil {
		t.Fatalf("LoadBoxfile: %v", err)
	}
	if bf.Name != "yaml-pack" {
		t.Errorf("Name: got %q, want yaml-pack", bf.Name)
	}
}

func TestLoadBoxfile_MalformedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Boxfile"), []byte("name: [bad yaml"), 0o644); err != nil {
		t.Fatalf("write Boxfile: %v", err)
	}

	_, _, err := LoadBoxfile(dir)
	if err == nil {
		t.Fatal("expected error for malformed Boxfile")
	}
	if !errors.Is(err, ErrBoxfileInvalid) {
		t.Errorf("expected ErrBoxfileInvalid, got %v", err)
	}
}
