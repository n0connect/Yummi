package config

import (
	"os"
	"testing"
	"time"
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
	if cfg.OllamaModel != "qwen3:14b" {
		t.Errorf("expected default OllamaModel qwen3:14b, got %q", cfg.OllamaModel)
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

func TestConfigSandboxExecTimeoutDefaultsToZero(t *testing.T) {
	unset(t, "YUMMI_SANDBOX_EXEC_TIMEOUT")

	cfg := Load()

	// Zero is deliberate here — it's the "unset" signal sandbox.Bridge.SetTimeout
	// treats as a no-op, leaving the Bridge's own generous default in place.
	if cfg.SandboxExecTimeout != 0 {
		t.Errorf("expected SandboxExecTimeout 0 (unset) by default, got %v", cfg.SandboxExecTimeout)
	}
}

func TestConfigSandboxExecTimeoutOverride(t *testing.T) {
	t.Setenv("YUMMI_SANDBOX_EXEC_TIMEOUT", "30m")

	cfg := Load()

	if cfg.SandboxExecTimeout != 30*time.Minute {
		t.Errorf("expected overridden SandboxExecTimeout 30m, got %v", cfg.SandboxExecTimeout)
	}
}

func TestConfigSandboxExecTimeoutInvalidFallsBackToZero(t *testing.T) {
	t.Setenv("YUMMI_SANDBOX_EXEC_TIMEOUT", "not-a-duration")

	cfg := Load()

	if cfg.SandboxExecTimeout != 0 {
		t.Errorf("expected invalid YUMMI_SANDBOX_EXEC_TIMEOUT to fall back to 0, got %v", cfg.SandboxExecTimeout)
	}
}

func TestConfigGatewayTLSOptIn(t *testing.T) {
	t.Setenv("YUMMI_GATEWAY_TLS", "true")

	cfg := Load()

	if !cfg.GatewayTLS {
		t.Error("expected GatewayTLS to be enabled")
	}
}

func TestConfigExplicitModelOverridesTier(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_TIER", "pro")
	t.Setenv("YUMMI_OLLAMA_MODEL", "custom:13b")

	cfg := Load()

	if cfg.OllamaModel != "custom:13b" {
		t.Errorf("expected explicit model to win, got %s", cfg.OllamaModel)
	}
	if cfg.OllamaTier != "pro" {
		t.Errorf("expected OllamaTier=pro, got %s", cfg.OllamaTier)
	}
}

func TestConfigTierSelectsModelWithoutExplicitOverride(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_TIER", "lite")
	t.Setenv("YUMMI_OLLAMA_MODEL", "")

	cfg := Load()
	if cfg.OllamaModel != "qwen3:4b" {
		t.Errorf("expected lite tier model, got %s", cfg.OllamaModel)
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
