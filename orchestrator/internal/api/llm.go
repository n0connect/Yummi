package api

import (
	"context"
	"net/http"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// HandleLLMStatus checks Ollama connectivity and returns health info.
func HandleLLMStatus(client *llm.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":  "not_configured",
				"details": "LLM client not initialized",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		err := client.Ping(ctx)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":  "disconnected",
				"details": "Ollama unreachable",
				"error":   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "connected",
			"details": "Ollama responding",
		})
	}
}
