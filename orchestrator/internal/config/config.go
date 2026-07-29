// Package config loads orchestrator configuration.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds orchestrator runtime configuration.
type Config struct {
	Host                string
	Port                int
	DatabasePath        string
	GatewaySocket       string
	SandboxContainer    string
	// SandboxExecTimeout bounds a single sandbox command's wall-clock time —
	// see sandbox.Bridge's defaultTimeout doc comment for why the default is
	// generous (real pentest tooling routinely runs long).
	SandboxExecTimeout time.Duration
	PersonaDir          string
	DockerSocket        string
	OllamaURL           string
	OllamaModel         string
	OllamaTier          string // "lite" | "pro" | "expert" — overrides OllamaModel if set
	OllamaFallbackModel string // optional lighter LOCAL model the agent loop degrades to when the primary repeatedly fails (empty = no downgrade, breaker still trips)
	OllamaEmbedModel    string // local Ollama embedding model for semantic retrieval (separate pull from OllamaModel); SemanticRetrieval degrades to StructuredRetrieval if this model isn't actually pulled
	TLSDir              string // directory holding mTLS certificates for orchestrator ↔ gateway
	GatewayTLS          bool   // opt-in until the Rust gateway IPC server supports TLS framing
}

// Load reads config from environment variables with sensible defaults.
func Load() *Config {
	home := os.Getenv("YUMMI_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".yummi")
	}

	port := 9080
	if p := os.Getenv("YUMMI_ORCHESTRATOR_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	cfg := &Config{
		Host:                envOr("YUMMI_ORCHESTRATOR_HOST", "127.0.0.1"),
		Port:                port,
		DatabasePath:        envOr("YUMMI_DB_PATH", filepath.Join(home, "sessions.db")),
		GatewaySocket:       envOr("YUMMI_GATEWAY_SOCKET", "/run/yummi/gateway.sock"),
		SandboxContainer:    envOr("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"),
		SandboxExecTimeout:  parseDuration(os.Getenv("YUMMI_SANDBOX_EXEC_TIMEOUT")), // 0 = use sandbox.Bridge's own default
		PersonaDir:          envOr("YUMMI_PERSONA_DIR", defaultPersonaDir()),
		DockerSocket:        envOr("YUMMI_DOCKER_SOCKET", defaultDockerSocket()),
		OllamaURL:           os.Getenv("YUMMI_OLLAMA_URL"), // empty = disabled
		OllamaModel:         envOr("YUMMI_OLLAMA_MODEL", "qwen3:8b"),
		OllamaTier:          strings.TrimSpace(os.Getenv("YUMMI_OLLAMA_TIER")),
		OllamaFallbackModel: strings.TrimSpace(os.Getenv("YUMMI_OLLAMA_FALLBACK_MODEL")),
		OllamaEmbedModel:    envOr("YUMMI_OLLAMA_EMBED_MODEL", "nomic-embed-text"),
		TLSDir:              envOr("YUMMI_TLS_DIR", filepath.Join(home, "tls")),
		GatewayTLS:          parseBool(os.Getenv("YUMMI_GATEWAY_TLS")),
	}

	// When an operator sets YUMMI_OLLAMA_TIER explicitly, use the tier's model
	// mapping instead of the default OllamaModel string. The resolution is done
	// by a tiny lookup table here so the config package stays independent of
	// the conductor package (no import cycle).
	if cfg.OllamaTier != "" {
		if model, ok := tierToModel(cfg.OllamaTier); ok {
			cfg.OllamaModel = model
		}
	}

	return cfg
}

// tierToModel mirrors conductor.DefaultTierRegistry without importing the
// conductor package. Keep this in sync with conductor/tier.go.
func tierToModel(tier string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "lite":
		return "qwen3:4b", true
	case "standard":
		return "qwen3:8b", true
	case "pro":
		return "qwen3:14b", true
	case "expert":
		return "qwen3:32b", true
	default:
		return "", false
	}
}

func defaultPersonaDir() string {
	candidates := []string{
		"persona/v0.1",
		"../persona/v0.1",
	}
	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && stat.IsDir() {
			return c
		}
	}
	return "../persona/v0.1"
}

// defaultDockerSocket returns the Docker socket path for the current platform.
// Checks the macOS Docker Desktop socket first, then the standard Linux path.
func defaultDockerSocket() string {
	candidates := []string{
		os.Getenv("HOME") + "/.docker/run/docker.sock",
		"/var/run/docker.sock",
		"/run/docker.sock",
	}
	for _, c := range candidates {
		if c == "/.docker/run/docker.sock" {
			continue // HOME was empty
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	// Fall back to the conventional Linux path even if it doesn't exist yet.
	return "/var/run/docker.sock"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseDuration parses a Go duration string (e.g. "10m", "600s"); an empty or
// invalid value returns 0, meaning "let the caller's own default stand" —
// mirrors SetTimeout's own zero-value-is-ignored contract.
func parseDuration(v string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return d
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// RequestTimeout returns the default HTTP request timeout.
// LLM inference (e.g. qwen3:8b) can exceed 30 s on CPU; 3 min gives headroom.
func (c *Config) RequestTimeout() time.Duration {
	return 3 * time.Minute
}
