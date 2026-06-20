package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("expected version 1, got %d", cfg.Version)
	}
	if cfg.Runtime.SandboxService == "" {
		t.Fatal("expected default n0 service")
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("version: 2\nruntime:\n  compose_file: my-compose.yml\n  sandbox_service: kali\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Version != 2 {
		t.Fatalf("expected version 2, got %d", cfg.Version)
	}
	if cfg.Runtime.ComposeFile != "my-compose.yml" {
		t.Fatalf("compose mismatch: %s", cfg.Runtime.ComposeFile)
	}
	if cfg.Runtime.SandboxService != "kali" {
		t.Fatalf("service mismatch: %s", cfg.Runtime.SandboxService)
	}
	if cfg.Runtime.PreferredShell == "" {
		t.Fatal("expected default preferred shell fallback")
	}
}
