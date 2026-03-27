package pkgmgr

import "time"

// PackageManifest is the schema for a binary adapter package.
type PackageManifest struct {
	Name        string
	Version     string
	Description string
	Adapter     AdapterConfig
	Primitives  []PrimitiveSpec
	Healthcheck *HealthcheckSpec
}

type AdapterConfig struct {
	Type       string   // "binary" only in MVP
	BinaryPath string   // may use {pb_dir} or {workspace}
	Args       []string
	SocketPath string // may use {workspace}
}

type PrimitiveSpec struct {
	Name        string
	Description string
	Intent      PrimitiveIntent
}

type PrimitiveIntent struct {
	Category   string
	SideEffect string
	RiskLevel  string
	Reversible bool
}

type HealthcheckSpec struct {
	Primitive string
	Params    map[string]any
	Timeout   time.Duration
}
