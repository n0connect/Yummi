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
	if cfg.OllamaModel != "gpt-oss:20b" {
		t.Errorf("expected default OllamaModel gpt-oss:20b, got %q", cfg.OllamaModel)
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

// TestConfigEinoModelOverridesDefaultBlank locks the "purely additive, zero
// behavior change unless set" contract: an operator who never touches
// YUMMI_EINO_MODEL_* must see cmd/orchestrator/main.go derive the exact same
// target it always has from YUMMI_OLLAMA_URL/MODEL — this test only proves
// Config itself defaults to blank/0 so main.go's fallback branch is the one
// that actually runs; main.go's own derivation logic is exercised by eino's
// own tests via Config.PlanLLM/ModelBaseURL, not here.
func TestConfigEinoModelOverridesDefaultBlank(t *testing.T) {
	unset(t, "YUMMI_EINO_MODEL_BASE_URL", "YUMMI_EINO_MODEL_NAME", "YUMMI_EINO_MODEL_NUM_CTX")

	cfg := Load()

	if cfg.EinoModelBaseURL != "" {
		t.Errorf("expected EinoModelBaseURL blank by default, got %q", cfg.EinoModelBaseURL)
	}
	if cfg.EinoModelName != "" {
		t.Errorf("expected EinoModelName blank by default, got %q", cfg.EinoModelName)
	}
	if cfg.EinoModelNumCtx != 0 {
		t.Errorf("expected EinoModelNumCtx 0 by default, got %d", cfg.EinoModelNumCtx)
	}
}

// TestConfigEinoModelOverridesRead proves the three env vars actually reach
// Config when an operator sets them (e.g. to point only the eino backend at
// a local LM Studio instance instead of Ollama).
func TestConfigEinoModelOverridesRead(t *testing.T) {
	t.Setenv("YUMMI_EINO_MODEL_BASE_URL", "http://localhost:1234/v1")
	t.Setenv("YUMMI_EINO_MODEL_NAME", "openai/gpt-oss-20b")
	t.Setenv("YUMMI_EINO_MODEL_NUM_CTX", "32768")

	cfg := Load()

	if cfg.EinoModelBaseURL != "http://localhost:1234/v1" {
		t.Errorf("EinoModelBaseURL = %q, want http://localhost:1234/v1", cfg.EinoModelBaseURL)
	}
	if cfg.EinoModelName != "openai/gpt-oss-20b" {
		t.Errorf("EinoModelName = %q, want openai/gpt-oss-20b", cfg.EinoModelName)
	}
	if cfg.EinoModelNumCtx != 32768 {
		t.Errorf("EinoModelNumCtx = %d, want 32768", cfg.EinoModelNumCtx)
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
	t.Setenv("YUMMI_OLLAMA_TIER", "expert")
	t.Setenv("YUMMI_OLLAMA_MODEL", "custom:13b")

	cfg := Load()

	if cfg.OllamaModel != "custom:13b" {
		t.Errorf("expected explicit model to win, got %s", cfg.OllamaModel)
	}
	if cfg.OllamaTier != "expert" {
		t.Errorf("expected OllamaTier=expert, got %s", cfg.OllamaTier)
	}
}

func TestConfigTierSelectsModelWithoutExplicitOverride(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_TIER", "standard")
	t.Setenv("YUMMI_OLLAMA_MODEL", "")

	cfg := Load()
	if cfg.OllamaModel != "gpt-oss:20b" {
		t.Errorf("expected standard tier model, got %s", cfg.OllamaModel)
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

func TestConfigOllamaNumCtxOverride(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_NUM_CTX", "131072")
	cfg := Load()
	if cfg.OllamaNumCtx != 131072 {
		t.Errorf("expected OllamaNumCtx=131072, got %d", cfg.OllamaNumCtx)
	}
}

func TestConfigOllamaNumCtxUnsetIsZero(t *testing.T) {
	unset(t, "YUMMI_OLLAMA_NUM_CTX")
	cfg := Load()
	if cfg.OllamaNumCtx != 0 {
		t.Errorf("expected OllamaNumCtx=0 when unset, got %d", cfg.OllamaNumCtx)
	}
}

func TestConfigOllamaNumCtxInvalidFallsBackToZero(t *testing.T) {
	t.Setenv("YUMMI_OLLAMA_NUM_CTX", "not-a-number")
	cfg := Load()
	if cfg.OllamaNumCtx != 0 {
		t.Errorf("expected OllamaNumCtx=0 for invalid input, got %d", cfg.OllamaNumCtx)
	}
}

func TestConfigOllamaThinkingTriState(t *testing.T) {
	unset(t, "YUMMI_OLLAMA_THINKING")
	if cfg := Load(); cfg.OllamaThinking != nil {
		t.Errorf("expected nil OllamaThinking when unset, got %v", *cfg.OllamaThinking)
	}

	t.Setenv("YUMMI_OLLAMA_THINKING", "true")
	cfg := Load()
	if cfg.OllamaThinking == nil || !*cfg.OllamaThinking {
		t.Errorf("expected OllamaThinking=true, got %v", cfg.OllamaThinking)
	}

	t.Setenv("YUMMI_OLLAMA_THINKING", "false")
	cfg = Load()
	if cfg.OllamaThinking == nil || *cfg.OllamaThinking {
		t.Errorf("expected OllamaThinking=false (explicitly, not nil), got %v", cfg.OllamaThinking)
	}
}

func TestConfigDataRetentionDisabledByDefault(t *testing.T) {
	unset(t, "YUMMI_DATA_RETENTION_DAYS")
	cfg := Load()
	if cfg.DataRetentionDays != 0 {
		t.Errorf("expected DataRetentionDays=0 (disabled) by default, got %d", cfg.DataRetentionDays)
	}
}

func TestConfigDataRetentionDaysOverride(t *testing.T) {
	t.Setenv("YUMMI_DATA_RETENTION_DAYS", "30")
	cfg := Load()
	if cfg.DataRetentionDays != 30 {
		t.Errorf("expected DataRetentionDays=30, got %d", cfg.DataRetentionDays)
	}
}

func TestConfigPortParseFallback(t *testing.T) {
	t.Setenv("YUMMI_ORCHESTRATOR_PORT", "not-a-number")
	cfg := Load()
	if cfg.Port != 9080 {
		t.Errorf("expected fallback to 9080 for bad port env, got %d", cfg.Port)
	}
}

// TestRequestAndChatTimeouts_SplitByCloudOverride locks the local/cloud
// timeout split: a local deployment needs a far larger HTTP budget than a
// cloud one (live-reproduced — a real local 20B model chat turn blew past
// the old flat 90s/3min pair), but an operator who HAS opted into
// YUMMI_EINO_MODEL_BASE_URL must get exactly the original cloud-sized
// values, unchanged — a local-model fix must never silently loosen the
// cloud path's own failure-detection latency.
func TestRequestAndChatTimeouts_SplitByCloudOverride(t *testing.T) {
	unset(t, "YUMMI_EINO_MODEL_BASE_URL")

	local := Load()
	if got, want := local.RequestTimeout(), 270*time.Second; got != want {
		t.Errorf("local RequestTimeout = %v, want %v", got, want)
	}
	if got, want := local.ChatTimeout(), 240*time.Second; got != want {
		t.Errorf("local ChatTimeout = %v, want %v", got, want)
	}

	t.Setenv("YUMMI_EINO_MODEL_BASE_URL", "https://openrouter.ai/api/v1")
	cloud := Load()
	if got, want := cloud.RequestTimeout(), 3*time.Minute; got != want {
		t.Errorf("cloud RequestTimeout = %v, want %v (must stay exactly the pre-existing value)", got, want)
	}
	if got, want := cloud.ChatTimeout(), 90*time.Second; got != want {
		t.Errorf("cloud ChatTimeout = %v, want %v (must stay exactly the pre-existing value)", got, want)
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
