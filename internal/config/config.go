// Package config provides configuration management for PrimitiveBox.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level PrimitiveBox configuration.
type Config struct {
	// Server configuration
	Server ServerConfig `yaml:"server" json:"server"`

	// Default sandbox settings
	Sandbox SandboxDefaults `yaml:"sandbox" json:"sandbox"`

	// Security settings
	Security SecurityConfig `yaml:"security" json:"security"`

	// Logging and audit
	Audit AuditConfig `yaml:"audit" json:"audit"`

	// Control plane persistence and background workers
	Control ControlConfig `yaml:"control" json:"control"`
}

// ServerConfig defines the RPC server settings.
type ServerConfig struct {
	Host string `yaml:"host" json:"host"` // Default: "localhost"
	Port int    `yaml:"port" json:"port"` // Default: 0 (auto-assign)
}

// SandboxDefaults defines default sandbox creation parameters.
type SandboxDefaults struct {
	Image       string  `yaml:"image" json:"image"`               // Default image
	CPULimit    float64 `yaml:"cpu_limit" json:"cpu_limit"`       // Default CPU cores
	MemoryLimit int64   `yaml:"memory_limit" json:"memory_limit"` // Default memory MB
	User        string  `yaml:"user" json:"user"`                 // Default user
	Timeout     int     `yaml:"timeout" json:"timeout"`           // Default command timeout in seconds
}

// SecurityConfig defines security settings.
type SecurityConfig struct {
	AllowedCommands []string `yaml:"allowed_commands" json:"allowed_commands"` // Command whitelist
	NetworkEnabled  bool     `yaml:"network_enabled" json:"network_enabled"`   // Default: false
	MaxSnapshots    int      `yaml:"max_snapshots" json:"max_snapshots"`       // Max checkpoints
}

// AuditConfig defines audit logging settings.
type AuditConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"` // Enable audit logging
	LogDir  string `yaml:"log_dir" json:"log_dir"` // Directory for audit logs
}

// ControlConfig defines control-plane persistence defaults.
type ControlConfig struct {
	DBPath                string `yaml:"db_path" json:"db_path"`
	ReaperIntervalSeconds int    `yaml:"reaper_interval_seconds" json:"reaper_interval_seconds"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		Sandbox: SandboxDefaults{
			Image:       "primitivebox-sandbox:latest",
			CPULimit:    1.0,
			MemoryLimit: 512,
			User:        "1000:1000",
			Timeout:     30,
		},
		Security: SecurityConfig{
			AllowedCommands: []string{}, // Empty = allow all (MVP)
			NetworkEnabled:  false,
			MaxSnapshots:    20,
		},
		Audit: AuditConfig{
			Enabled: true,
			LogDir:  ".primitivebox/audit",
		},
		Control: ControlConfig{
			DBPath:                filepath.Join(home, ".primitivebox", "controlplane.db"),
			ReaperIntervalSeconds: 30,
		},
	}
}

// LoadFromFile loads configuration from a YAML file.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
