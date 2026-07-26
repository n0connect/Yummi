package config

import (
	"os"
	"strings"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	unset(t, "YUMMI_ORCHESTRATOR_HOST", "YUMMI_ORCHESTRATOR_PORT", "YUMMI_DB_PATH",
		"YUMMI_GATEWAY_SOCKET", "YUMMI_SANDBOX_CONTAINER", "YUMMI_PERSONA_DIR",
		"YUMMI_OLLAMA_URL", "YUMMI_OLLAMA_MODEL", "YUMMI_OLLAMA_TIER", "YUMMI_TLS_DIR",
		"YUMMI_GATEWAY_TLS", "YUMMI_OLLAMA_EMBED_MODEL")

	cfg := Load()

	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected default host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != 9080 {
		t.Errorf("expected default port 9080, got %d", cfg.Port)
	}
	if cfg.OllamaURL != "" {
		t.Errorf("expected OllamaURL empty by default, got %s", cfg.OllamaURL)
	}
	if cfg.OllamaModel == "" {
		t.Error("expected a default OllamaModel tag")
	}
	if cfg.TLSDir == "" {
		t.Error("expected a default TLSDir")
	}
	if cfg.GatewayTLS {
		t.Error("expected GatewayTLS disabled by default")
	}
	if cfg.OllamaEmbedModel != "nomic-embed-text" {
		t.Errorf("expected default OllamaEmbedModel nomic-embed-text, got %q", cfg.OllamaEmbedModel)
	}
}

func TestConfigOllamaEmbedModelOverride(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_EMBED_MODEL", "custom-embed-model")

	cfg := Load()

	if cfg.OllamaEmbedModel != "custom-embed-model" {
		t.Errorf("expected overridden OllamaEmbedModel, got %q", cfg.OllamaEmbedModel)
	}
}

func TestConfigGatewayTLSOptIn(t *testing.T) {
	t.Setenv("YUMMI_GATEWAY_TLS", "true")

	cfg := Load()

	if !cfg.GatewayTLS {
		t.Error("expected GatewayTLS to be enabled")
	}
}

func TestConfigTierOverride(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_TIER", "pro")
	t.Setenv("YUMMI_OLLAMA_MODEL", "qwen3:4b") // will be overridden by tier

	cfg := Load()

	if !strings.Contains(strings.ToLower(cfg.OllamaModel), "14b") {
		t.Errorf("expected pro tier to map to qwen3:14b, got %s", cfg.OllamaModel)
	}
	if cfg.OllamaTier != "pro" {
		t.Errorf("expected OllamaTier=pro, got %s", cfg.OllamaTier)
	}
}

func TestConfigTierUnknownKeepsModel(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_TIER", "not-a-tier")
	t.Setenv("YUMMI_OLLAMA_MODEL", "custom:13b")

	cfg := Load()

	if cfg.OllamaModel != "custom:13b" {
		t.Errorf("unknown tier must not rewrite OllamaModel, got %s", cfg.OllamaModel)
	}
}

func TestConfigPortParseFallback(t *testing.T) {
	t.Setenv("YUMMI_ORCHESTRATOR_PORT", "not-a-number")
	cfg := Load()
	if cfg.Port != 9080 {
		t.Errorf("expected fallback to 9080 for bad port env, got %d", cfg.Port)
	}
}

// unset clears the given env vars for the duration of the test. Uses t.Setenv
// with empty string (Go's testing env helper doesn't expose Unsetenv).
func unset(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		original := os.Getenv(k)
		_ = os.Unsetenv(k)
		t.Cleanup(func() {
			if original != "" {
				_ = os.Setenv(k, original)
			}
		})
	}
}
