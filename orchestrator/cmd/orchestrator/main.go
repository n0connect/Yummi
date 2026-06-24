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
	"syscall"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/api"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/config"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/health"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/session"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
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

	assistant, err := conductor.NewEngine(cfg.PersonaDir, cfg.OllamaURL, cfg.OllamaModel)
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

	bridge := sandbox.NewBridge(cfg.SandboxContainer)
	gatewayClient := gateway.NewClient(cfg.GatewaySocket)
	if cfg.GatewayTLS && tlsClientConfig != nil {
		gatewayClient = gatewayClient.WithTLS(tlsClientConfig)
	}
	llmClient := llm.NewClient(cfg.OllamaURL, cfg.OllamaModel)

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
	// The processor is wrapped so that every completed execution feeds its
	// output back into the conductor's session history. This closes the
	// agentic loop: LLM proposes → user approves → command runs → LLM sees
	// the result on the next turn → LLM proposes the next step.
	exec := executor.New(bridge, gatewayClient, ks)
	baseProcessor := exec.ProcessFunc()
	executionStore.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		output, err := baseProcessor(ctx, rec)
		if assistant != nil && rec.SessionID != "" {
			var errText string
			if err != nil {
				errText = err.Error()
			}
			assistant.AppendObservation(rec.SessionID, rec.Command, output, errText)
		}
		return output, err
	})

	// Agentic task loop: store + runner over the same execution pipeline.
	// Commands run by the loop flow through executionStore, so they get gateway
	// scope checks, sandbox exec, persistence and ws events for free.
	taskStore, err := agent.NewStore(db)
	if err != nil {
		slog.Error("agent task store init failed", "error", err)
		os.Exit(1)
	}
	var taskRunner *agent.Runner
	// skillReg is also exposed read-only via the TRACE API, so it lives at this
	// scope (nil-safe: skill.Registry methods tolerate a nil receiver).
	var skillReg *skill.Registry
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
			retrieval := knowledge.NewStructuredRetrieval(ks)

			// Sub-agents: specialist Runners sharing the same Store + Knowledge +
			// execution pipeline. Built with no SubAgents of their own, capping
			// handoff depth at 1 (no recursion).
			subAgents := map[string]*agent.Runner{}
			subDefs, derr := agent.LoadSubAgentDefs(filepath.Join(cfg.PersonaDir, "subagents"))
			if derr != nil {
				slog.Warn("sub-agent defs load failed; continuing without delegation", "error", derr)
			}
			for _, d := range subDefs {
				subAgents[d.Role] = agent.NewRunner(
					taskStore,
					assistant.LLM(),
					assistant.Validator(),
					agent.NewStoreExecer(executionStore, 10*time.Minute),
					agent.Config{
						Persona:   d.Persona(),
						Knowledge: ks,
						Retrieval: retrieval,
						Skills:    skillReg.Subset(d.Skills),
					},
				)
			}
			if len(subAgents) > 0 {
				slog.Info("sub-agents registered", "roles", len(subAgents))
			}

			taskRunner = agent.NewRunner(
				taskStore,
				assistant.LLM(),
				assistant.Validator(),
				agent.NewStoreExecer(executionStore, 10*time.Minute),
				agent.Config{ // defaults: budget 15, ask_permission mode
					Persona:   agentPersona,
					Knowledge: ks,
					Retrieval: retrieval,
					Skills:    skillReg,
					SubAgents: subAgents,
				},
			)
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

	router := api.NewRouter(sm, ks, skillReg, cfg, assistant, approvalStore, executionStore, bridge, gatewayClient, logBuf, llmClient, agg, taskStore, taskRunner)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
		// ReadTimeout covers header + body read; 30s is generous for any request.
		ReadTimeout: 30 * time.Second,
		// WriteTimeout must accommodate LLM inference latency. qwen3:8b can take
		// 20-45 seconds on CPU-heavy loads. Setting to 120s gives headroom while
		// still bounding runaway connections. Per-route tighter timeouts (chat
		// handler uses a 90s context) enforce the actual LLM budget.
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

	slog.Info("orchestrator stopped")
}
