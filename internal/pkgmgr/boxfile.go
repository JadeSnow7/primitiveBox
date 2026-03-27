package pkgmgr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	// ErrBoxfileNotFound is returned when no Boxfile is found in the given directory.
	ErrBoxfileNotFound = errors.New("Boxfile not found")
	// ErrBoxfileInvalid is returned when a Boxfile fails validation.
	ErrBoxfileInvalid = errors.New("invalid Boxfile")
)

// boxfileNameRe enforces a safe lowercase slug for package names.
var boxfileNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Boxfile is the declarative package descriptor users author alongside their adapters.
// Users place a file named "Boxfile", "boxfile.yaml", or "boxfile.yml" in their package
// directory and install it with: pb pkg install ./my-data-pack
type Boxfile struct {
	Name        string              `yaml:"name"`
	Version     string              `yaml:"version"`
	Description string              `yaml:"description"`
	Adapter     BoxfileAdapter      `yaml:"adapter"`
	Primitives  []BoxfilePrimitive  `yaml:"primitives"`
	Healthcheck *BoxfileHealthcheck `yaml:"healthcheck"`
	Bootstrap   *BoxfileBootstrap   `yaml:"bootstrap"`
}

// BoxfileAdapter describes the adapter process for the package.
type BoxfileAdapter struct {
	// Type must be "binary" or "mcp".
	Type string `yaml:"type"`
	// BinaryPath is the path to the adapter binary. May be relative to the Boxfile
	// directory or use the {pb_dir} and {workspace} template tokens.
	BinaryPath string `yaml:"binary_path"`
	// SocketPath is the Unix socket the adapter will listen on.
	// May use the {workspace} template token.
	SocketPath string `yaml:"socket_path"`
	// Args are extra arguments forwarded to the binary after --socket <path>.
	Args []string `yaml:"args"`
	// MCPCommand is the stdio command for type: mcp adapters (wrapped via pb-mcp-bridge).
	MCPCommand string `yaml:"mcp_command"`
}

// BoxfilePrimitive declares a primitive exposed by the adapter.
type BoxfilePrimitive struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Intent      BoxfileIntent `yaml:"intent"`
}

// BoxfileIntent mirrors PrimitiveIntent in the YAML surface.
type BoxfileIntent struct {
	Category   string `yaml:"category"`
	SideEffect string `yaml:"side_effect"`
	RiskLevel  string `yaml:"risk_level"`
	Reversible bool   `yaml:"reversible"`
}

// BoxfileHealthcheck describes how to verify the adapter is healthy after launch.
type BoxfileHealthcheck struct {
	Primitive string         `yaml:"primitive"`
	Params    map[string]any `yaml:"params"`
	// Timeout is a Go duration string, e.g. "5s". Defaults to 5s.
	Timeout string `yaml:"timeout"`
}

// BoxfileBootstrap declares static files to be injected into the sandbox workspace
// on sandbox creation. No code is executed; files are plain filesystem copies.
type BoxfileBootstrap struct {
	Files []BoxfileBootstrapFile `yaml:"files"`
}

// BoxfileBootstrapFile maps a source file (relative to the Boxfile dir) to a
// destination path inside the sandbox workspace.
type BoxfileBootstrapFile struct {
	Src string `yaml:"src"` // relative to Boxfile directory
	Dst string `yaml:"dst"` // relative to sandbox workspace root
}

// ParseBoxfile decodes Boxfile YAML. Unknown fields are rejected to surface typos early.
func ParseBoxfile(data []byte) (Boxfile, error) {
	var bf Boxfile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&bf); err != nil {
		return Boxfile{}, fmt.Errorf("%w: %v", ErrBoxfileInvalid, err)
	}
	return bf, nil
}

// LoadBoxfile loads and parses the Boxfile from dir.
// It tries "Boxfile", "boxfile.yaml", "boxfile.yml" in that order.
// Returns the parsed Boxfile, the resolved path, and any error.
func LoadBoxfile(dir string) (Boxfile, string, error) {
	for _, name := range []string{"Boxfile", "boxfile.yaml", "boxfile.yml"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Boxfile{}, "", fmt.Errorf("read %s: %w", name, err)
		}
		bf, err := ParseBoxfile(data)
		if err != nil {
			return Boxfile{}, "", err
		}
		return bf, path, nil
	}
	return Boxfile{}, "", ErrBoxfileNotFound
}

// ValidateBoxfile checks that a Boxfile satisfies all structural and safety rules.
// Call this before converting to a PackageManifest.
func ValidateBoxfile(bf Boxfile) error {
	if bf.Name == "" {
		return fmt.Errorf("%w: name is required", ErrBoxfileInvalid)
	}
	if !boxfileNameRe.MatchString(bf.Name) {
		return fmt.Errorf("%w: name %q must match ^[a-z][a-z0-9-]{0,62}$", ErrBoxfileInvalid, bf.Name)
	}
	if bf.Version == "" {
		return fmt.Errorf("%w: version is required", ErrBoxfileInvalid)
	}

	switch bf.Adapter.Type {
	case "binary":
		if bf.Adapter.BinaryPath == "" {
			return fmt.Errorf("%w: adapter.binary_path is required for type 'binary'", ErrBoxfileInvalid)
		}
	case "mcp":
		if bf.Adapter.BinaryPath == "" && bf.Adapter.MCPCommand == "" {
			return fmt.Errorf("%w: adapter.binary_path or adapter.mcp_command is required for type 'mcp'", ErrBoxfileInvalid)
		}
	case "":
		return fmt.Errorf("%w: adapter.type is required", ErrBoxfileInvalid)
	default:
		return fmt.Errorf("%w: adapter.type must be 'binary' or 'mcp', got %q", ErrBoxfileInvalid, bf.Adapter.Type)
	}

	if bf.Adapter.SocketPath == "" {
		return fmt.Errorf("%w: adapter.socket_path is required", ErrBoxfileInvalid)
	}

	for _, spec := range bf.Primitives {
		if spec.Name == "" {
			return fmt.Errorf("%w: primitive name must not be empty", ErrBoxfileInvalid)
		}
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(spec.Name, prefix) {
				return fmt.Errorf("%w: primitive %q starts with reserved prefix %q",
					ErrReservedNamespace, spec.Name, prefix)
			}
		}
	}

	if bf.Bootstrap != nil {
		for i, file := range bf.Bootstrap.Files {
			if file.Src == "" {
				return fmt.Errorf("%w: bootstrap.files[%d].src is required", ErrBoxfileInvalid, i)
			}
			if file.Dst == "" {
				return fmt.Errorf("%w: bootstrap.files[%d].dst is required", ErrBoxfileInvalid, i)
			}
			// Reject path traversal in destination — dst must stay inside workspace.
			if strings.Contains(file.Dst, "..") {
				return fmt.Errorf("%w: bootstrap.files[%d].dst %q contains path traversal",
					ErrBoxfileInvalid, i, file.Dst)
			}
			// dst must be relative — no leading slash so it can't escape workspace.
			if filepath.IsAbs(file.Dst) {
				return fmt.Errorf("%w: bootstrap.files[%d].dst %q must be relative",
					ErrBoxfileInvalid, i, file.Dst)
			}
		}
	}

	return nil
}

// BoxfileToManifest converts a validated Boxfile to a PackageManifest ready for the
// installer. baseDir is the directory that contained the Boxfile; it is used to
// resolve relative binary paths to absolute paths at parse time.
func BoxfileToManifest(bf Boxfile, baseDir string) (PackageManifest, error) {
	adapterType := bf.Adapter.Type

	binaryPath := bf.Adapter.BinaryPath
	if adapterType == "mcp" && binaryPath == "" {
		// When no explicit binary is given for an MCP adapter, delegate to pb-mcp-bridge.
		binaryPath = "{pb_dir}/pb-mcp-bridge"
	}

	// Resolve relative binary paths against baseDir (only when no template tokens present).
	if binaryPath != "" && !strings.Contains(binaryPath, "{") && !filepath.IsAbs(binaryPath) {
		abs, err := filepath.Abs(filepath.Join(baseDir, binaryPath))
		if err != nil {
			return PackageManifest{}, fmt.Errorf("resolve binary path: %w", err)
		}
		binaryPath = abs
	}

	socketPath := bf.Adapter.SocketPath
	// Relative socket paths become workspace-relative template paths.
	if socketPath != "" && !strings.Contains(socketPath, "{") && !filepath.IsAbs(socketPath) {
		socketPath = "{workspace}/" + socketPath
	}

	var primitives []PrimitiveSpec
	for _, p := range bf.Primitives {
		primitives = append(primitives, PrimitiveSpec{
			Name:        p.Name,
			Description: p.Description,
			Intent: PrimitiveIntent{
				Category:   p.Intent.Category,
				SideEffect: p.Intent.SideEffect,
				RiskLevel:  p.Intent.RiskLevel,
				Reversible: p.Intent.Reversible,
			},
		})
	}

	var hc *HealthcheckSpec
	if bf.Healthcheck != nil && bf.Healthcheck.Primitive != "" {
		timeout := 5 * time.Second
		if bf.Healthcheck.Timeout != "" {
			if d, err := time.ParseDuration(bf.Healthcheck.Timeout); err == nil {
				timeout = d
			}
		}
		hc = &HealthcheckSpec{
			Primitive: bf.Healthcheck.Primitive,
			Params:    bf.Healthcheck.Params,
			Timeout:   timeout,
		}
	}

	args := append([]string(nil), bf.Adapter.Args...)
	if adapterType == "mcp" && bf.Adapter.MCPCommand != "" {
		args = append([]string{"--cmd", bf.Adapter.MCPCommand}, args...)
	}

	return PackageManifest{
		Name:        bf.Name,
		Version:     bf.Version,
		Description: bf.Description,
		Adapter: AdapterConfig{
			Type:       adapterType,
			BinaryPath: binaryPath,
			SocketPath: socketPath,
			Args:       args,
		},
		Primitives:  primitives,
		Healthcheck: hc,
	}, nil
}
