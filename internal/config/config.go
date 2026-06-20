// Package config loads and validates user configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultComposeFile is the fallback compose file path.
	DefaultComposeFile = "docker-compose.yml"
	// DefaultSandboxService is the compose service name used by yummi.
	DefaultSandboxService = "sandbox"
	// DefaultPreferredShell is the shell used inside sandbox.
	DefaultPreferredShell = "/bin/bash"
)

// RuntimeConfig stores runtime-specific settings.
type RuntimeConfig struct {
	ComposeFile    string `yaml:"compose_file" json:"compose_file"`
	SandboxService string `yaml:"sandbox_service" json:"sandbox_service"`
	PreferredShell string `yaml:"preferred_shell" json:"preferred_shell"`
}

// Config stores user-facing yummi configuration.
type Config struct {
	Version int           `yaml:"version" json:"version"`
	Runtime RuntimeConfig `yaml:"runtime" json:"runtime"`
}

// HomeDir returns the configured yummi home directory path.
func HomeDir() string {
	if custom := os.Getenv("YUMMI_HOME"); custom != "" {
		return custom
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return ".yummi"
	}
	return filepath.Join(base, ".yummi")
}

// ConfigPath returns the default config path.
func ConfigPath() string {
	return filepath.Join(HomeDir(), "config.yaml")
}

// Default returns default configuration values.
func Default() Config {
	return Config{
		Version: 1,
		Runtime: RuntimeConfig{
			ComposeFile:    DefaultComposeFile,
			SandboxService: DefaultSandboxService,
			PreferredShell: DefaultPreferredShell,
		},
	}
}

// Load reads config from disk and applies defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = ConfigPath()
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(buf, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Runtime.ComposeFile == "" {
		cfg.Runtime.ComposeFile = DefaultComposeFile
	}
	if cfg.Runtime.SandboxService == "" {
		cfg.Runtime.SandboxService = DefaultSandboxService
	}
	if cfg.Runtime.PreferredShell == "" {
		cfg.Runtime.PreferredShell = DefaultPreferredShell
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return cfg, nil
}
