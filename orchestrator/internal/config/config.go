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
	Host                 string
	Port                 int
	OperatorAllowedPeers string // exact socket peers; never trust forwarded IP headers
	SandboxPolicyAddr    string // separate sandbox read-only policy listener, empty disables it
	DatabasePath         string
	GatewaySocket        string
	SandboxContainer     string
	// SandboxDesktopAddr is host:port for the sandbox container's own
	// websockify bridge (see docker-compose.yml — the same unified "sandbox"
	// service the agent execs into also runs the desktop stack) —
	// HandleDesktopWSBridge dials this on every /ws/desktop connection.
	// Defaults to the compose service's own DNS name; if the sandbox
	// container itself is down, the dial simply fails with a clean "desktop
	// sandbox unreachable" message per-connection rather than the
	// orchestrator refusing to start.
	SandboxDesktopAddr string
	// SandboxExecTimeout bounds a single sandbox command's wall-clock time —
	// see sandbox.Bridge's defaultTimeout doc comment for why the default is
	// generous (real pentest tooling routinely runs long).
	SandboxExecTimeout time.Duration
	PersonaDir         string
	AutoSkillDir       string // writable store for operator-approved generated skills
	DockerSocket       string
	OllamaURL          string
	OllamaModel        string
	OllamaTier         string // "lite" | "standard" | "pro" | "expert"; used when no explicit model is set
	// OllamaNumCtx overrides the context window a custom OllamaModel would
	// otherwise be silently capped at (Pro tier's fixed 16384) — see
	// conductor.TierOverrides. 0 = no override, use the resolved tier's own
	// value. Irrelevant to a named tier (lite/standard/pro/expert), which
	// already picks a context window on purpose.
	OllamaNumCtx int
	// OllamaThinking overrides a custom OllamaModel's thinking-mode default
	// (Pro tier's "false"). nil = no override.
	OllamaThinking      *bool
	OllamaFallbackModel string // optional lighter LOCAL model the agent loop degrades to when the primary repeatedly fails (empty = no downgrade, breaker still trips)
	OllamaEmbedModel    string // local Ollama embedding model for semantic retrieval (separate pull from OllamaModel); SemanticRetrieval degrades to StructuredRetrieval if this model isn't actually pulled
	// EinoModelBaseURL/EinoModelName/EinoModelNumCtx let the Eino backend's
	// own ReAct loop run against a DIFFERENT OpenAI-compatible server/model
	// than the one Ollama/OllamaModel drives — e.g. a local LM Studio, vLLM,
	// or llama.cpp server instead of Ollama, for a larger/more capable
	// tool-calling model. The conductor (chat/intent classification) and
	// this override are independent: conductor always needs a working
	// Ollama-native client regardless of this setting, since chat/intent
	// handling never goes through the agent backend. All three are blank/0
	// by default, in which case cmd/orchestrator/main.go derives the exact
	// same values it always has (Ollama's own OpenAI-compat endpoint) — this
	// is purely additive, zero behavior change until an operator sets it.
	// EinoModelBaseURL must include any path prefix the target server needs
	// (e.g. "http://localhost:1234/v1") — unlike the Ollama derivation, this
	// is not assumed to be Ollama, so no "/v1" is auto-appended.
	EinoModelBaseURL string
	EinoModelName    string
	EinoModelAPIKey  string
	EinoProvider     string // "ollama" | "openrouter" | "openai" (auto-detected if blank)
	EinoModelNumCtx  int
	// EinoModelMaxTokens caps a single ReAct turn's completion size against a
	// metered cloud target (OpenRouter/OpenAI) — see
	// eino.resolveMaxTokens's doc comment for why this exists: left at the
	// default 0, a cloud provider can default max_tokens to the model's
	// absolute output ceiling and reject the whole call if the key's
	// remaining credit can't cover that worst case, even on a turn that
	// never needed anywhere near it (live-observed with gpt-4.1 via
	// OpenRouter). 0 = eino.resolveMaxTokens's own built-in default for a
	// cloud target, uncapped for a local one.
	EinoModelMaxTokens int
	TLSDir             string // directory holding mTLS certificates for orchestrator ↔ gateway
	GatewayTLS         bool   // opt-in until the Rust gateway IPC server supports TLS framing
	// DataRetentionDays enables internal/retention's background pruning of
	// old chat_messages/agent_activity/executions rows when > 0. 0 (the
	// default) disables it entirely — see internal/retention's package doc
	// for exactly what is and is never pruned.
	DataRetentionDays int
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

	explicitOllamaModel := strings.TrimSpace(os.Getenv("YUMMI_OLLAMA_MODEL"))
	cfg := &Config{
		Host:                 envOr("YUMMI_ORCHESTRATOR_HOST", "127.0.0.1"),
		Port:                 port,
		OperatorAllowedPeers: envOr("YUMMI_OPERATOR_ALLOWED_PEERS", "127.0.0.1/32,::1/128"),
		SandboxPolicyAddr:    strings.TrimSpace(os.Getenv("YUMMI_SANDBOX_POLICY_ADDR")),
		DatabasePath:         envOr("YUMMI_DB_PATH", filepath.Join(home, "sessions.db")),
		GatewaySocket:        envOr("YUMMI_GATEWAY_SOCKET", "/run/yummi/gateway.sock"),
		SandboxContainer:     envOr("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"),
		SandboxDesktopAddr:   envOr("YUMMI_SANDBOX_DESKTOP_ADDR", "sandbox:6080"),
		SandboxExecTimeout:   parseDuration(os.Getenv("YUMMI_SANDBOX_EXEC_TIMEOUT")), // 0 = use sandbox.Bridge's own default
		PersonaDir:           envOr("YUMMI_PERSONA_DIR", defaultPersonaDir()),
		AutoSkillDir:         envOr("YUMMI_AUTO_SKILL_DIR", filepath.Join(home, "skills")),
		DockerSocket:         envOr("YUMMI_DOCKER_SOCKET", defaultDockerSocket()),
		OllamaURL:            os.Getenv("YUMMI_OLLAMA_URL"), // empty = disabled
		OllamaModel:          envOr("YUMMI_OLLAMA_MODEL", "gpt-oss:20b"),
		OllamaTier:           strings.TrimSpace(os.Getenv("YUMMI_OLLAMA_TIER")),
		OllamaNumCtx:         parseIntOr(os.Getenv("YUMMI_OLLAMA_NUM_CTX"), 0),
		OllamaThinking:       parseOptionalBool(os.Getenv("YUMMI_OLLAMA_THINKING")),
		OllamaFallbackModel:  strings.TrimSpace(os.Getenv("YUMMI_OLLAMA_FALLBACK_MODEL")),
		OllamaEmbedModel:     envOr("YUMMI_OLLAMA_EMBED_MODEL", "nomic-embed-text"),
		EinoModelBaseURL:     strings.TrimSpace(os.Getenv("YUMMI_EINO_MODEL_BASE_URL")),
		EinoModelName:        strings.TrimSpace(os.Getenv("YUMMI_EINO_MODEL_NAME")),
		EinoModelAPIKey:      strings.TrimSpace(os.Getenv("YUMMI_EINO_MODEL_API_KEY")),
		EinoProvider:         strings.ToLower(strings.TrimSpace(os.Getenv("YUMMI_EINO_PROVIDER"))),
		EinoModelNumCtx:      parseIntOr(os.Getenv("YUMMI_EINO_MODEL_NUM_CTX"), 0),
		EinoModelMaxTokens:   parseIntOr(os.Getenv("YUMMI_EINO_MODEL_MAX_TOKENS"), 0),
		TLSDir:               envOr("YUMMI_TLS_DIR", filepath.Join(home, "tls")),
		GatewayTLS:           parseBool(os.Getenv("YUMMI_GATEWAY_TLS")),
		DataRetentionDays:    parseIntOr(os.Getenv("YUMMI_DATA_RETENTION_DAYS"), 0),
	}

	// A directly configured model is the most specific choice. Otherwise an
	// explicit tier selects its model; with neither set, gpt-oss:20b is the default.
	if explicitOllamaModel == "" && cfg.OllamaTier != "" {
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
	case "standard":
		return "gpt-oss:20b", true
	case "expert":
		return "gpt-oss:120b", true
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

// parseIntOr parses v as an int; an empty or invalid value returns fallback.
func parseIntOr(v string, fallback int) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// parseOptionalBool is parseBool's tri-state sibling: nil when v is unset
// (meaning "no opinion, use the caller's own default"), otherwise a pointer
// to the parsed value. Needed because plain parseBool can't distinguish
// "explicitly set to false" from "never set" — both would read as false.
func parseOptionalBool(v string) *bool {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	b := parseBool(v)
	return &b
}

// usesCloudModel reports whether an operator has opted the conductor/agent
// loop into a cloud override (YUMMI_EINO_MODEL_BASE_URL) — the same
// "full stop, not just for Eino" decision conductor.NewEngineWithOverrides'
// CloudBaseURL plumbing already makes (see cmd/orchestrator/main.go). Local
// and cloud HTTP timeouts in this file are derived from this single check
// so they can never drift relative to each other, and so a local-model fix
// never silently loosens the cloud path's own failure-detection latency.
func (c *Config) usesCloudModel() bool {
	return c.EinoModelBaseURL != ""
}

// UsesLocalModel is usesCloudModel's exported negation, for callers outside
// this package (main.go's agent.Config.LocalModel wiring) that need the
// same single source of truth rather than re-deriving it from the raw
// EinoModelBaseURL field.
func (c *Config) UsesLocalModel() bool {
	return !c.usesCloudModel()
}

// RequestTimeout returns the router-wide non-WebSocket request timeout
// (TimeoutUnlessWebSocket middleware, applied to every route).
// 3 minutes is already generous for a cloud call. A local model needs more:
// live-reproduced on a real 20B model under genuine load, a chat turn that
// chains conductor's intent classification + CreateAndPlan's planning call
// (+ its one parse-retry) blew well past the OLD flat 90s chat-handler
// budget — this router-wide ceiling must be raised in lockstep with
// ChatTimeout below, or it fires first and the inner fix is moot.
func (c *Config) RequestTimeout() time.Duration {
	if c.usesCloudModel() {
		return 3 * time.Minute
	}
	return 270 * time.Second
}

// ChatTimeout returns the /api/v1/chat handler's own context deadline —
// see RequestTimeout's doc comment for why local needs far more budget than
// cloud, and HandleChat's doc comment for why this must stay a few seconds
// below RequestTimeout (the router-wide ceiling) rather than equal to it.
func (c *Config) ChatTimeout() time.Duration {
	if c.usesCloudModel() {
		return 90 * time.Second
	}
	return 240 * time.Second
}

// ResolvedEinoProvider returns the effective provider for Eino ("ollama", "openrouter", "openai").
// Auto-detects based on BaseURL or APIKey if not explicitly set.
func (c *Config) ResolvedEinoProvider() string {
	if c.EinoProvider != "" {
		return c.EinoProvider
	}
	if strings.Contains(c.EinoModelBaseURL, "openrouter.ai") {
		return "openrouter"
	}
	if strings.Contains(c.EinoModelBaseURL, "api.openai.com") {
		return "openai"
	}
	if c.EinoModelAPIKey != "" && c.EinoModelAPIKey != "ollama" {
		return "openai"
	}
	return "ollama"
}
