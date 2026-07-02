package api

import (
	"context"
	"net/http"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// HandleLLMStatus checks Ollama connectivity and returns health info. resilient
// is the agent loop's circuit-breaker wrapper (nil when the agent loop is
// disabled, e.g. persona load failed) — when present its breaker/latency
// snapshot is attached under "resilience" so operators can see primary/
// fallback model, open-breaker state, and consecutive-failure count without
// digging through logs.
func HandleLLMStatus(client *llm.Client, resilient *llm.Resilient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			resp := map[string]any{
				"status":  "not_configured",
				"details": "LLM client not initialized",
			}
			attachResilience(resp, resilient)
			writeJSON(w, http.StatusOK, resp)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		err := client.Ping(ctx)
		if err != nil {
			resp := map[string]any{
				"status":  "disconnected",
				"details": "Ollama unreachable",
				"error":   err.Error(),
			}
			attachResilience(resp, resilient)
			writeJSON(w, http.StatusOK, resp)
			return
		}

		resp := map[string]any{
			"status":  "connected",
			"details": "Ollama responding",
		}
		attachResilience(resp, resilient)
		writeJSON(w, http.StatusOK, resp)
	}
}

func attachResilience(resp map[string]any, resilient *llm.Resilient) {
	if resilient == nil {
		return
	}
	resp["resilience"] = resilient.Status()
}
