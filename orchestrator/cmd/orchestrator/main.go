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

	assistant, err := conductor.NewEngineWithOverrides(cfg.PersonaDir, cfg.OllamaURL, cfg.OllamaModel, conductor.TierOverrides{
		NumCtx:   cfg.OllamaNumCtx,
		Thinking: cfg.OllamaThinking,
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
	if cfg.OllamaURL != "" {
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

			// Sub-agents: specialist Runners sharing the same Store + Knowledge +
			// execution pipeline. Built with no SubAgents of their own, capping
			// handoff depth at 1 (no recursion).
			subAgents := map[string]*agent.Runner{}
			subAgentHints := map[string]string{}
			subDefs, derr := agent.LoadSubAgentDefs(filepath.Join(cfg.PersonaDir, "subagents"))
			if derr != nil {
				slog.Warn("sub-agent defs load failed; continuing without delegation", "error", derr)
			}
			for _, d := range subDefs {
				subAgentHints[d.Role] = d.RoutingHint
				subAgents[d.Role] = agent.NewRunner(
					taskStore,
					agentLLM,
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					agent.Config{
						Persona:      d.Persona(),
						Knowledge:    ks,
						Retrieval:    retrieval,
						Skills:       skillReg.Subset(d.Skills),
						SkillCurator: skillCuratorFn,
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
				slog.Info("sub-agents registered", "roles", len(subAgents))
			}

			// Agent backend selection: graph (default, tag-protocol state
			// machine — best for small/local models) or eino (native
			// tool-calling — best for larger models with reliable
			// function-calling). Runtime env var read here rather than a
			// build tag, so operators can switch by restarting the
			// container, not rebuilding it; make conf's YUMMI_AGENT_BACKEND
			// question just pre-seeds this for convenience.
			//
			// Eino's own model target defaults to mirroring the conductor's
			// Ollama config exactly as before (same URL rewritten to its
			// OpenAI-compat path, same model, same resolved context window)
			// — zero behavior change for anyone who hasn't set the three
			// EinoModel* overrides. An operator CAN point only the agent
			// loop at a different OpenAI-compatible server/model (e.g. a
			// local LM Studio instance running a larger tool-calling model)
			// via YUMMI_EINO_MODEL_BASE_URL/YUMMI_EINO_MODEL_NAME/
			// YUMMI_EINO_MODEL_NUM_CTX — see config.Config's doc comments.
			// The conductor keeps using Ollama regardless of this setting:
			// chat/intent classification never goes through the agent
			// backend, so it has no dependency on which model drives tasks.
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

			switch backend := strings.ToLower(os.Getenv("YUMMI_AGENT_BACKEND")); backend {
			case "eino":
				taskRunner = eino.NewEinoRunner(
					taskStore,
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					eino.Config{
						StepBudget:    15,
						ApprovalMode:  agent.ModeAskPermission,
						ModelBaseURL:  einoModelBaseURL,
						ModelName:     einoModelName,
						ModelAPIKey:   cfg.EinoModelAPIKey,
						ModelNumCtx:   einoModelNumCtx,
						ModelThinking: resolvedThinking,
						// Built from the SAME target as the ReAct loop above
						// (not the conductor's agentLLM) so the goal-part
						// audit call runs on whichever model is actually
						// driving this task, self-consistent regardless of
						// whether that's Ollama or an override.
						PlanLLM:       eino.NewOpenAIChatClient(einoModelBaseURL, einoModelName, cfg.EinoModelAPIKey, einoModelNumCtx, 0),
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
				taskRunner = agent.NewRunner(
					taskStore,
					agentLLM,
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					agent.Config{ // defaults: budget 15, ask_permission mode
						Persona:       agentPersona,
						Knowledge:     ks,
						Retrieval:     retrieval,
						Skills:        skillReg,
						SubAgents:     subAgents,
						SubAgentHints: subAgentHints,
						SkillCurator:  skillCuratorFn,
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

	router := api.NewRouter(sm, ks, skillReg, cfg, assistant, approvalStore, executionStore, bridge, gatewayClient, logBuf, llmClient, resilientLLM, agg, taskStore, taskRunner, curatorStore)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
		// ReadTimeout covers header + body read; 30s is generous for any request.
		ReadTimeout: 30 * time.Second,
		// WriteTimeout must accommodate LLM inference latency, which can run
		// 20-45+ seconds on CPU-heavy loads. Setting to 120s gives headroom
		// while still bounding runaway connections. Per-route tighter timeouts
		// (chat handler uses a 90s context) enforce the actual LLM budget.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		slog.Info("orchestrator starting", "addr", addr, "version", "v0.1.0")
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
