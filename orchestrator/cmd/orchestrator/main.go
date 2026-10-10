// Package main is the entry point for the orchestrator.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/agent/eino"
	"github.com/n0connect/yummi/orchestrator/internal/api"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/config"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/health"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
	"github.com/n0connect/yummi/orchestrator/internal/retention"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/sandboxmcp"
	"github.com/n0connect/yummi/orchestrator/internal/session"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
	"github.com/n0connect/yummi/orchestrator/internal/tlsutil"
)

func main() {
	baseHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logBuf := api.NewLogBuffer(baseHandler)
	logger := slog.New(logBuf)
	slog.SetDefault(logger)

	cfg := config.Load()

	// Load-or-generate local mTLS material and build the gateway client.
	// If TLS generation fails, fall back to plaintext Unix socket.
	var tlsClientConfig *tls.Config
	if tlsBundle, err := tlsutil.LoadOrGenerate(cfg.TLSDir); err != nil {
		slog.Warn("mTLS bundle unavailable; orchestrator ↔ gateway TLS channel disabled",
			"error", err, "tls_dir", cfg.TLSDir)
	} else {
		slog.Info("mTLS bundle loaded",
			"tls_dir", cfg.TLSDir,
			"ca_subject", tlsBundle.CACert.Subject.CommonName,
			"leaf_subject", tlsBundle.LeafCert.Subject.CommonName,
			"leaf_expires", tlsBundle.LeafCert.NotAfter)
		pool := x509.NewCertPool()
		pool.AddCert(tlsBundle.CACert)
		tlsClientConfig = &tls.Config{
			Certificates: []tls.Certificate{tlsBundle.LeafPair},
			RootCAs:      pool,
			ServerName:   "orchestrator.yummi.local",
			MinVersion:   tls.VersionTLS13,
		}
	}

	db, err := session.OpenDB(cfg.DatabasePath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	sm := session.NewManager(db)

	// knowledge owns the world model (targets, findings, procedural memory).
	// It creates/migrates its tables here so every writer shares one store.
	ks, err := knowledge.NewStore(db)
	if err != nil {
		slog.Error("knowledge store init failed", "error", err)
		os.Exit(1)
	}

	// Resolved once here so the Eino backend (below) can inject the identical
	// effective num_ctx as an outbound-request option — conductor.Engine
	// resolves and applies its own copy of this internally but doesn't expose
	// it, and Eino's OpenAI-compatible model calls need their own explicit
	// value (see eino.NewOllamaChatModel's doc comment).
	resolvedTier := conductor.DefaultTierRegistry().Resolve(cfg.OllamaModel)
	resolvedNumCtx := resolvedTier.NumCtx
	if cfg.OllamaNumCtx > 0 {
		resolvedNumCtx = cfg.OllamaNumCtx
	}
	// resolvedThinking mirrors NewEngineWithOverrides' own resolution
	// (DefaultTierRegistry tier default, overridden by an explicit
	// YUMMI_OLLAMA_THINKING) so the Eino backend below gets the SAME
	// resolved value the conductor actually applies to its own calls,
	// instead of the two silently disagreeing — see eino.NewOllamaChatModel's
	// doc comment for why false maps to "medium" reasoning_effort there,
	// not a literal off.
	resolvedThinking := resolvedTier.Thinking
	if cfg.OllamaThinking != nil {
		resolvedThinking = *cfg.OllamaThinking
	}

	// Eino's own model target: defaults to mirroring the conductor's Ollama
	// config exactly (same URL rewritten to its OpenAI-compat path, same
	// model, same resolved context window) when none of the three
	// YUMMI_EINO_MODEL_* overrides are set — zero behavior change for
	// anyone who hasn't opted in. Resolved here (before conductor
	// construction, not down in the Eino backend switch below) because the
	// conductor's own cloud-override decision right below needs the same
	// values: an explicitly-set base URL means an operator has opted into a
	// cloud model, full stop, not just "for Eino."
	einoModelBaseURL := cfg.EinoModelBaseURL
	if einoModelBaseURL == "" {
		einoModelBaseURL = strings.TrimSuffix(cfg.OllamaURL, "/") + "/v1"
	}
	einoModelName := cfg.EinoModelName
	if einoModelName == "" {
		einoModelName = cfg.OllamaModel
	}
	einoModelNumCtx := cfg.EinoModelNumCtx
	if einoModelNumCtx <= 0 {
		einoModelNumCtx = resolvedNumCtx
	}
	// einoModelMaxTokens: no tier-resolved fallback needed, unlike num_ctx
	// above — 0 already means exactly the right thing on its own
	// (eino.resolveMaxTokens applies its own built-in cap for a metered
	// cloud target and stays uncapped for a local one).
	einoModelMaxTokens := cfg.EinoModelMaxTokens

	// CloudBaseURL, when the operator explicitly set YUMMI_EINO_MODEL_BASE_URL,
	// is a MANDATORY replacement for Ollama, not an addition alongside it: an
	// operator who has already opted an agent backend into a cloud model gets
	// no privacy benefit from also running Ollama purely for chat/intent
	// classification, since that classification step hands the same goal
	// text to the same cloud model anyway the moment a task starts — see
	// conductor.ChatBackend's doc comment. cfg.EinoModelBaseURL (not the
	// derived einoModelBaseURL, which is never empty) is the right check
	// here: an UNSET override already means "Eino mirrors Ollama exactly,"
	// so there is no redundancy to eliminate in that case — conductor
	// staying on Ollama is already correct. This also means the graph
	// backend's own agent loop (which reuses assistant.LLM() as primaryLLM
	// below) moves to the same cloud model automatically, not just Eino's
	// ReAct loop.
	assistant, err := conductor.NewEngineWithOverrides(cfg.PersonaDir, cfg.OllamaURL, cfg.OllamaModel, conductor.TierOverrides{
		NumCtx:   cfg.OllamaNumCtx,
		Thinking: cfg.OllamaThinking,
		CloudBaseURL: func() string {
			if cfg.EinoModelBaseURL == "" {
				return ""
			}
			return einoModelBaseURL
		}(),
		CloudModel:  einoModelName,
		CloudAPIKey: cfg.EinoModelAPIKey,
	})
	if err != nil {
		slog.Warn("assistant persona load failed; running without conductor", "error", err, "persona_dir", cfg.PersonaDir)
	}
	approvalStore := conductor.NewApprovalStore(20 * time.Minute)
	executionStore := execution.NewStoreWithDB(db)

	// Graceful shutdown context is also used by background workers.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Background purge of expired approval proposals every 5 minutes.
	approvalStore.StartPurger(ctx, 5*time.Minute)
	// Background data-retention pruning — disabled unless the operator opts
	// in via YUMMI_DATA_RETENTION_DAYS (see internal/retention's package
	// doc for exactly what is and is never pruned). A 6h check interval is
	// plenty of resolution for a day-granularity retention window.
	retention.StartPurger(ctx, db, cfg.DataRetentionDays, 6*time.Hour)

	bridge := sandbox.NewBridge(cfg.SandboxContainer)
	bridge.SetTimeout(cfg.SandboxExecTimeout) // no-op when unset — Bridge keeps its own generous default
	gatewayClient := gateway.NewClient(cfg.GatewaySocket)
	if cfg.GatewayTLS && tlsClientConfig != nil {
		gatewayClient = gatewayClient.WithTLS(tlsClientConfig)
	}
	// Persistent MCP session into the sandbox container — reached via the
	// same docker-exec invocation shape sandbox.Bridge.Exec uses, just held
	// open (-i) instead of one-shot-buffered. Feeds the executor's MCP
	// dispatch path (processMCP) regardless of which agent backend is active;
	// only the Eino backend currently issues MCP tool calls.
	sandboxMCPClient := sandboxmcp.NewClient(cfg.SandboxContainer)
	// preloadNumCtx is the larger of the conductor's own resolved context and
	// any explicit Eino override THAT TARGETS THE SAME OLLAMA INSTANCE (an
	// unset EinoModelBaseURL, or one explicitly equal to the derived Ollama
	// OpenAI-compat URL — when Eino points at a genuinely different server,
	// e.g. LM Studio, its num_ctx is irrelevant to this Ollama client and
	// must not inflate its preload for no reason). Applied via
	// WithOptions below instead of NewClient's DefaultQwen3Options hardcoded
	// 16384. Without this, Preload() — which runs at startup, before any
	// real chat request — could be the first call to touch a not-yet-loaded
	// model, permanently fixing its context window: Ollama allocates a
	// model's KV cache once, sized by whichever request loads it, and every
	// later request asking for a different num_ctx (the conductor's own
	// resolved value, OR Eino's explicit override) is silently served
	// against that already-fixed size instead of getting what it asked for.
	// Live-observed twice: once with Preload's old hardcoded 16384 beating a
	// differently-resolved tier value, and again with Preload's (correctly
	// resolved but smaller) value beating Eino's own larger explicit
	// EinoModelNumCtx.
	preloadNumCtx := resolvedNumCtx
	einoTargetsSameOllama := cfg.EinoModelBaseURL == "" ||
		cfg.EinoModelBaseURL == strings.TrimSuffix(cfg.OllamaURL, "/")+"/v1"
	// planLLMTimeout: explicit, path-aware — see PlanLLM's own construction
	// comment below for why this must NOT be a shared default. Zero for the
	// cloud path keeps eino.NewOpenAIChatClient's own tighter 60s default
	// (a real OpenRouter call never needs more); only a genuinely local
	// target gets the extra slack local inference requires.
	planLLMTimeout := time.Duration(0)
	if einoTargetsSameOllama {
		planLLMTimeout = 5 * time.Minute
	}
	if einoTargetsSameOllama && cfg.EinoModelNumCtx > preloadNumCtx {
		preloadNumCtx = cfg.EinoModelNumCtx
	}
	llmClient := llm.NewClient(cfg.OllamaURL, cfg.OllamaModel).WithOptions(func() llm.Options {
		o := llm.DefaultQwen3Options()
		o.NumCtx = preloadNumCtx
		return o
	}())

	// Semantic retrieval: an optional local embedding model (separate Ollama
	// pull from the chat model) lets procedural memory rank by similarity
	// instead of exact tool-name match. embedClient stays nil (and
	// SemanticRetrieval degrades to StructuredRetrieval on first use) if the
	// operator hasn't pulled cfg.OllamaEmbedModel — this is never fatal.
	var embedClient *llm.Client
	if cfg.OllamaURL != "" && cfg.OllamaEmbedModel != "" {
		embedClient = llmClient.WithEmbedModel(cfg.OllamaEmbedModel)
		ks.SetEmbedder(embedClient, cfg.OllamaEmbedModel)
	}

	// Preload the model into VRAM before serving requests — guarantees a single
	// cold-start. Failure is non-fatal; the model lazy-loads on the first chat.
	// With keep_alive="-1", once loaded it stays resident for the process lifetime,
	// so this runs exactly once regardless of how many requests follow.
	//
	// Skipped when the conductor has moved to a cloud backend
	// (cfg.EinoModelBaseURL set — see conductor.NewEngineWithOverrides'
	// CloudBaseURL above): nothing in that configuration calls the Ollama
	// chat model anymore, so preloading (and keeping resident) a
	// multi-gigabyte model into VRAM for zero actual use is exactly the
	// redundant local load an operator who opted into a cloud model
	// shouldn't pay for. embedClient above is untouched — semantic
	// retrieval's embedding model is a genuinely separate, small, local-only
	// feature with no cloud equivalent in this architecture, unrelated to
	// the chat-model redundancy this guards against.
	if cfg.OllamaURL != "" && cfg.EinoModelBaseURL == "" {
		preloadCtx, preloadCancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer preloadCancel()
		if err := llmClient.Preload(preloadCtx); err != nil {
			slog.Warn("ollama preload failed; model will be lazy-loaded on first request",
				"error", err, "model", cfg.OllamaModel)
		} else {
			slog.Info("ollama model preloaded to VRAM", "model", cfg.OllamaModel)
		}
	}

	// Wire real command execution: sandbox bridge + gateway scope check.
	// Single-command output enters conductor history only through the UI's
	// typed from_feedback turn after execution threat review. Appending raw
	// processor output here would both duplicate feedback and bypass that hold.
	exec := executor.New(bridge, gatewayClient, ks, sandboxMCPClient)
	baseProcessor := exec.ProcessFunc()
	executionStore.SetProcessor(baseProcessor)

	// Agentic task loop: store + runner over the same execution pipeline.
	// Commands run by the loop flow through executionStore, so they get gateway
	// scope checks, sandbox exec, persistence and ws events for free.
	taskStore, err := agent.NewStore(db)
	if err != nil {
		slog.Error("agent task store init failed", "error", err)
		os.Exit(1)
	}

	// Auto-skill-creation: candidate skills drafted from successful task
	// trajectories, quarantined pending operator approval (see
	// internal/skillcurator). curatorStore init failure is non-fatal —
	// degrades to auto-skill-creation disabled, same as other optional stores.
	curatorStore, cerr := skillcurator.NewStore(db)
	if cerr != nil {
		slog.Warn("skill curator store init failed; auto-skill-creation disabled", "error", cerr)
		curatorStore = nil
	}

	var taskRunner agent.TaskRunner
	var curatorWorker *skillcurator.Worker
	// skillReg is also exposed read-only via the TRACE API, so it lives at this
	// scope (nil-safe: skill.Registry methods tolerate a nil receiver).
	var skillReg *skill.Registry
	// mcpSchemaHints is also consulted by the conductor's single-command
	// approve path (HandleChatApprove, see api.NewRouter's call site below)
	// so it lives at this scope — nil when the agent loop is disabled or the
	// sandbox-mcp catalog was unavailable at startup, which just means that
	// path keeps its pre-existing shell-only behavior (InferMCPToolFromShellGuess
	// is nil-map-safe: a nil/empty map never matches, same as "not found").
	var mcpSchemaHints map[string]string
	// resilientLLM is the agent loop's circuit-breaker wrapper. Kept at its
	// concrete type (not just the agent.LLM interface it also satisfies) so the
	// /api/v1/llm/status handler can read its breaker/latency snapshot; nil when
	// the agent loop is disabled (persona load failed).
	var resilientLLM *llm.Resilient
	if assistant != nil && assistant.LLM() != nil {
		// The agent persona is document-driven (no hard-coded prose). Without it
		// the loop cannot run, so disable the agent rather than improvise.
		agentPersona, perr := agent.LoadAgentPersona(cfg.PersonaDir)
		if perr != nil {
			slog.Error("agent persona load failed; agent loop disabled", "error", perr, "persona_dir", cfg.PersonaDir)
		} else {
			// Skills: declarative playbooks injected into prompts (no exec path).
			var serr error
			skillReg, serr = skill.LoadDir(filepath.Join(cfg.PersonaDir, "skills"))
			if serr != nil {
				slog.Warn("skills load failed; continuing without skills", "error", serr)
				skillReg = skill.New(nil)
			}
			// Operator-approved generated skills live in a separate writable
			// directory. The shipped persona remains read-only and auditable.
			if autoReg, aerr := skill.LoadDirWithOrigin(cfg.AutoSkillDir, "auto"); aerr == nil {
				for _, approved := range autoReg.All() {
					if err := skillReg.Upsert(approved); err != nil {
						slog.Warn("approved skill load failed", "name", approved.Name, "error", err)
					}
				}
			} else if !os.IsNotExist(aerr) {
				slog.Warn("approved skills load failed", "dir", cfg.AutoSkillDir, "error", aerr)
			}
			structuredRetrieval := knowledge.NewStructuredRetrieval(ks)
			var retrieval knowledge.RetrievalStrategy = structuredRetrieval
			if embedClient != nil {
				retrieval = knowledge.NewSemanticRetrieval(ks, embedClient, cfg.OllamaEmbedModel, structuredRetrieval)
			}

			// Resilience: wrap the agent loop's LLM in a circuit breaker + EMA
			// latency tracker, with an optional downgrade to a lighter LOCAL model
			// (privacy-first — no cloud routing). On repeated primary failures the
			// loop degrades to the fallback instead of stalling; the breaker also
			// stops hammering a downed Ollama. With no fallback configured it still
			// fails fast and tracks health.
			primaryLLM := assistant.LLM()
			var fallbackChat llm.ChatFunc
			fallbackName := ""
			if fb := cfg.OllamaFallbackModel; fb != "" && fb != primaryLLM.Model() {
				fallbackChat = llm.NewClient(cfg.OllamaURL, fb).Chat
				fallbackName = fb
				slog.Info("agent LLM fallback configured", "primary", primaryLLM.Model(), "fallback", fb)
			}
			resilientLLM = llm.NewResilient(
				primaryLLM.Chat, fallbackChat, primaryLLM.Model(), fallbackName, 3, 60*time.Second)
			var agentLLM agent.LLM = resilientLLM

			// Auto-skill creation runs through a bounded, shutdown-aware worker;
			// task completion only performs a non-blocking enqueue.
			curatorWorker = skillcurator.NewWorker(
				skillcurator.NewCurator(curatorStore, resilientLLM.Chat, skillReg), 32, 2*time.Minute)
			curatorWorker.Start(ctx)
			skillCuratorFn := func(_ context.Context, traj skillcurator.Trajectory) {
				curatorWorker.Submit(traj)
			}

			// MCP_TOOL parity for the graph backend (and, since every sub-agent
			// below is always an agent.Runner regardless of top-level
			// YUMMI_AGENT_BACKEND, for Eino's sub-agents too): a graph Runner
			// previously had zero access to the 54 cataloged pentest tools or
			// the 7 terminal-session verbs sandbox-mcp exposes — only raw shell
			// via RUN_COMMAND. graphMCPExec reuses the exact same
			// execution.Store/gateway/threatscan pipeline StoreExecer already
			// goes through; mcpCatalogLine is ONE compact names-only line (no
			// per-tool JSON schemas — see agent.Config.MCPCatalog's doc comment
			// on why that would cost real context on every single step render).
			// Best-effort: an unreachable sandbox at startup just leaves
			// MCPCatalog empty, degrading to the pre-existing shell-only
			// behavior rather than failing agent construction.
			graphMCPExec := agent.NewStoreMCPExecer(executionStore, 10*time.Minute)
			var mcpCatalogLine string
			mcpCatalogLine, mcpSchemaHints = buildMCPCatalog(ctx, sandboxMCPClient)
			if mcpCatalogLine != "" {
				slog.Info("graph backend: MCP_TOOL catalog loaded", "tool_count", strings.Count(mcpCatalogLine, ",")+1)
			}

			// Sub-agent ROLE DEFINITIONS are backend-agnostic (persona, skills,
			// routing hint) — only the Runner each role is built on differs.
			// Loaded once here; the actual per-role Runner/EinoRunner
			// construction happens inside the backend switch below, on
			// whichever backend the top-level task also uses. See
			// agent.SubAgentRunner's doc comment for why this split matters:
			// a HANDOFF must never cross from one engine's semantics into the
			// other's.
			subAgentHints := map[string]string{}
			subDefs, derr := agent.LoadSubAgentDefs(filepath.Join(cfg.PersonaDir, "subagents"))
			if derr != nil {
				slog.Warn("sub-agent defs load failed; continuing without delegation", "error", derr)
			}
			for _, d := range subDefs {
				subAgentHints[d.Role] = d.RoutingHint
			}

			// Agent backend selection: graph (default, tag-protocol state
			// machine — best for small/local models) or eino (native
			// tool-calling — best for larger models with reliable
			// function-calling). Runtime env var read here rather than a
			// build tag, so operators can switch by restarting the
			// container, not rebuilding it; make conf's YUMMI_AGENT_BACKEND
			// question just pre-seeds this for convenience.
			//
			// einoModelBaseURL/einoModelName/einoModelNumCtx are resolved once,
			// early (see the block right before conductor.NewEngineWithOverrides
			// above) — defaults to mirroring the conductor's Ollama config
			// exactly when none of the three YUMMI_EINO_MODEL_* overrides are
			// set (zero behavior change for anyone who hasn't opted in), or to
			// an explicit override target otherwise. When an override IS set,
			// the conductor was ALREADY pointed at this same target above —
			// not left on Ollama — so chat/intent classification and the
			// agent's own task execution run on one self-consistent model, not
			// two silently different ones.
			switch backend := strings.ToLower(os.Getenv("YUMMI_AGENT_BACKEND")); backend {
			case "eino":
				// Sub-agents built on the SAME backend as the top-level task —
				// an Eino-driven task now delegates to Eino-driven specialists,
				// never silently into the graph engine's semantics. Shares one
				// PlanLLM client (stateless — just config + an http.Client)
				// across every sub-agent and the top-level runner below, rather
				// than constructing one per role.
				einoPlanLLM := eino.NewOpenAIChatClient(einoModelBaseURL, einoModelName, cfg.EinoModelAPIKey, einoModelNumCtx, einoModelMaxTokens, planLLMTimeout)
				subAgents := map[string]agent.SubAgentRunner{}
				for _, d := range subDefs {
					subAgents[d.Role] = eino.NewEinoRunner(
						taskStore,
						assistant.Validator(),
						agent.NewStoreExecer(executionStore, 10*time.Minute),
						eino.Config{
							ApprovalMode:   agent.ModeAskPermission,
							ModelBaseURL:   einoModelBaseURL,
							ModelName:      einoModelName,
							ModelAPIKey:    cfg.EinoModelAPIKey,
							ModelNumCtx:    einoModelNumCtx,
							ModelMaxTokens: einoModelMaxTokens,
							ModelThinking:  resolvedThinking,
							PlanLLM:        einoPlanLLM,
							Persona:        d.Persona(),
							Knowledge:      ks,
							MCPExec:        agent.NewStoreMCPExecer(executionStore, 10*time.Minute),
							MCPList:        sandboxMCPClient,
							Retrieval:      retrieval,
							Skills:         skillReg.Subset(d.Skills),
							SkillCurator:   skillCuratorFn,
							// Mirrors the graph backend's own sub-agent StepBudget
							// (40 vs the top-level default 15) — see that
							// field's comment in the default: branch below for
							// why a focused subtask needs more room, not less.
							// SubAgents is left unset (empty): caps handoff
							// depth at exactly 1, same convention either
							// backend relies on.
							StepBudget: 40,
						},
					)
				}
				if len(subAgents) > 0 {
					slog.Info("sub-agents registered", "roles", len(subAgents), "backend", "eino")
				}
				taskRunner = eino.NewEinoRunner(
					taskStore,
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					eino.Config{
						StepBudget:     15,
						ApprovalMode:   agent.ModeAskPermission,
						ModelBaseURL:   einoModelBaseURL,
						ModelName:      einoModelName,
						ModelAPIKey:    cfg.EinoModelAPIKey,
						ModelNumCtx:    einoModelNumCtx,
						ModelMaxTokens: einoModelMaxTokens,
						ModelThinking:  resolvedThinking,
						// Built from the SAME target as the ReAct loop above
						// (not the conductor's agentLLM) so the goal-part
						// audit call runs on whichever model is actually
						// driving this task, self-consistent regardless of
						// whether that's Ollama or an override.
						//
						// Timeout is explicit and path-aware, not left at
						// NewOpenAIChatClient's shared 60s default: a LOCAL
						// target (einoTargetsSameOllama) genuinely needs
						// minutes on a large audit prompt — live-reproduced
						// elsewhere in this file's history (see llm.Client's
						// own defaultHTTPTimeout comment) — while a real
						// OpenRouter/cloud override returns in seconds
						// regardless of prompt size, so it keeps the
						// constructor's own tighter default rather than
						// inheriting local's extra slack it never needs.
						PlanLLM:       einoPlanLLM,
						Persona:       agentPersona,
						Knowledge:     ks,
						MCPExec:       agent.NewStoreMCPExecer(executionStore, 10*time.Minute),
						MCPList:       sandboxMCPClient,
						Retrieval:     retrieval,
						Skills:        skillReg,
						SubAgents:     subAgents,
						SubAgentHints: subAgentHints,
						SkillCurator:  skillCuratorFn,
						// Matches the graph backend's own SubAgentTimeout bump
						// (see its comment below): sub-agents get a higher
						// StepBudget than the top-level loop's default 15, so
						// they need proportionally more wall clock too.
						SubAgentTimeout: 20 * time.Minute,
					},
				)
				slog.Info("agent backend: eino", "model", einoModelName, "base_url", einoModelBaseURL, "provider", cfg.ResolvedEinoProvider())
			default:
				// Sub-agents: specialist Runners sharing the same Store +
				// Knowledge + execution pipeline. Built with no SubAgents of
				// their own, capping handoff depth at 1 (no recursion).
				subAgents := map[string]agent.SubAgentRunner{}
				for _, d := range subDefs {
					subAgents[d.Role] = agent.NewRunner(
						taskStore,
						agentLLM,
						assistant.Validator(),
						agent.NewStoreExecer(executionStore, 10*time.Minute),
						agent.Config{
							Persona:        d.Persona(),
							Knowledge:      ks,
							Retrieval:      retrieval,
							Skills:         skillReg.Subset(d.Skills),
							SkillCurator:   skillCuratorFn,
							MCPExec:        graphMCPExec,
							MCPCatalog:     mcpCatalogLine,
							MCPSchemaHints: mcpSchemaHints,
							LocalModel:     cfg.UsesLocalModel(),
							// Sub-agents get a higher step budget than the top-level
							// agent's default (15): a HANDOFF delegates one focused,
							// narrower subtask, so the specialist should be able to
							// run more tool calls against it than the generalist top-
							// level loop needs for open-ended recon. Paired with the
							// parent's SubAgentTimeout bump below, which is the actual
							// wall-clock ceiling this step budget runs against.
							StepBudget: 40,
							// TokenBudget scales with the model's actual configured
							// context window (resolvedNumCtx) instead of the
							// package's hardcoded 6000 fallback, which was sized for
							// nothing in particular and identical across lite/pro/
							// expert tiers regardless of their real 8192/16384/32768
							// windows. Half the window is reserved as compression
							// headroom for persona/skills/plan overhead and model
							// output; the other half is the raw-observation budget
							// NeedsCompression watches. A higher step budget (40 vs
							// 15) makes this even more load-bearing for sub-agents.
							TokenBudget: resolvedNumCtx / 2,
						},
					)
				}
				if len(subAgents) > 0 {
					slog.Info("sub-agents registered", "roles", len(subAgents), "backend", "graph")
				}
				taskRunner = agent.NewRunner(
					taskStore,
					agentLLM,
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					agent.Config{ // defaults: budget 15, ask_permission mode
						Persona:        agentPersona,
						Knowledge:      ks,
						Retrieval:      retrieval,
						Skills:         skillReg,
						SubAgents:      subAgents,
						SubAgentHints:  subAgentHints,
						SkillCurator:   skillCuratorFn,
						MCPExec:        graphMCPExec,
						MCPCatalog:     mcpCatalogLine,
						MCPSchemaHints: mcpSchemaHints,
						LocalModel:     cfg.UsesLocalModel(),
						// Room for the sub-agents' own StepBudget:40 above to actually
						// run to completion — the default 10m ceiling was sized for
						// StepBudget:15 (matches the top-level loop); a sub-agent with
						// a larger budget needs proportionally more wall clock or the
						// timeout, not the step count, becomes the real limiter.
						SubAgentTimeout: 20 * time.Minute,
						// Same resolvedNumCtx-scaled TokenBudget as the sub-agent
						// Config above — see that field's comment. Previously this
						// was left unset (defaulting to a flat 6000 regardless of
						// tier), the single biggest reason long multi-step tasks
						// degraded unpredictably: on lite (8192 ctx) 6000 raw-
						// observation tokens plus untracked persona/plan/skills
						// overhead could already be past the real window; on
						// expert (32768 ctx) the same 6000 triggered lossy
						// LLM-resummarization passes far earlier than necessary,
						// discarding specific technical detail (exact versions,
						// paths, header values) for no reason — a real quality
						// cost paid on every long session regardless of backend.
						TokenBudget: resolvedNumCtx / 2,
					},
				)
				slog.Info("agent backend: graph")
			}
			if resumed, rerr := taskRunner.ResumeRecovered(); rerr != nil {
				slog.Error("recovered task resume failed", "error", rerr, "resumed", resumed)
			} else if resumed > 0 {
				slog.Info("recovered tasks resumed", "count", resumed)
			}
		}
	}

	// Health metric aggregator — collects every 5 s into a ring buffer.
	// Ping sandbox and gateway with tight timeouts.
	agg := health.New(
		cfg.OllamaURL,
		func(c context.Context) bool { return bridge.Ping(c) == nil },
		gatewayClient,
	)
	go agg.Start(ctx)

	router := api.NewRouter(sm, ks, skillReg, cfg, assistant, approvalStore, executionStore, bridge, gatewayClient, logBuf, llmClient, resilientLLM, agg, taskStore, taskRunner, curatorStore, curatorWorker, sandboxMCPClient, mcpSchemaHints)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
		// ReadTimeout covers header + body read; 30s is generous for any request.
		ReadTimeout: 30 * time.Second,
		// WriteTimeout must exceed the router's own RequestTimeout middleware
		// (cfg.RequestTimeout(), applied in routes.go) with margin for response
		// serialization, or the server would cut the TCP write before that
		// middleware even gets to respond — derived from it, not an
		// independent literal, so the local/cloud split in config.go can
		// never leave this one lagging behind.
		WriteTimeout: cfg.RequestTimeout() + 30*time.Second,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		slog.Info("orchestrator starting", "addr", addr, "version", "v1.0.1b")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	if curatorWorker != nil {
		_ = curatorWorker.Wait(shutdownCtx)
	}

	slog.Info("orchestrator stopped")
}

// buildMCPCatalog renders the sandbox-mcp tool catalog for the graph
// backend: catalogLine is ONE compact, names-only line for the step prompt —
// see agent.Config.MCPCatalog's doc comment for why this must stay terse
// rather than a per-tool schema dump (graph re-renders its entire system
// prompt from scratch every step, with no conversation history to amortize
// the cost against). schemaHints is NOT prompt-injected at all — it is kept
// in memory and consulted only when a specific tool call actually fails
// (see node_tool.go's MCP branch), to turn a terse schema-validation
// rejection into something the model can self-correct from on the next
// attempt. Live-observed need for this: jinx-20b guessed term_open's session
// field was named "name"; the real field is "label", and the raw validator
// error ("unexpected additional properties") never said so — the model had
// no way to recover without this. Best-effort throughout: a failed or
// not-yet-up sandbox at startup just returns ("", nil), leaving MCP_TOOL
// unavailable for this process lifetime (degrades to graph's pre-existing
// shell-only behavior) — never fatal to orchestrator startup.
func buildMCPCatalog(ctx context.Context, client *sandboxmcp.Client) (catalogLine string, schemaHints map[string]string) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tools, err := client.ListTools(listCtx)
	if err != nil {
		slog.Warn("mcp catalog unavailable for graph backend; MCP_TOOL disabled until next restart", "error", err)
		return "", nil
	}
	names := make([]string, 0, len(tools))
	hints := make(map[string]string, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
		if fields := schemaFieldNames(t.InputSchema); len(fields) > 0 {
			hints[t.Name] = strings.Join(fields, ", ")
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", "), hints
}

// schemaFieldNames extracts property names from an MCP tool's client-side
// InputSchema — a map[string]any JSON Schema object per mcp.Tool's own doc
// comment ("From the client, this field will hold the default JSON
// marshaling of the server's input schema"). Returns nil on any shape this
// doesn't recognize rather than guessing.
func schemaFieldNames(inputSchema any) []string {
	schema, ok := inputSchema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
