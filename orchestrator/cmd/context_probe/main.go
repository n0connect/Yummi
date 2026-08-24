// Command context_probe is a transparent reverse proxy in front of an
// OpenAI-compatible chat-completions server (LM Studio, vLLM, Ollama's own
// /v1 surface, ...), used to actively observe context growth turn-by-turn
// across a long-running agentic conversation. It exists because neither the
// Eino backend nor the target server's own logs surface this by default —
// without it, "is context pressure actually building up in this session"
// is a guess, not a measurement.
//
// For every /chat/completions request it logs, per turn:
//   - request shape: message count, tool count, raw request byte size
//   - response: the SERVER'S OWN reported usage.prompt_tokens/
//     completion_tokens/total_tokens — llama.cpp/LM Studio's real count,
//     not an estimate — plus finish_reason and whether tool_calls fired
//   - wall-clock latency for the call
//
// Usage:
//
//	go run ./cmd/context_probe -listen :8899 -target http://localhost:1234
//
// Then point EINO_MODEL_BASE_URL (or YUMMI_EINO_MODEL_BASE_URL) at
// http://localhost:8899/v1 instead of the real server's own address — every
// call the agent makes is logged here before being forwarded unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var turnCounter atomic.Int64

func main() {
	listen := flag.String("listen", ":8899", "address to listen on")
	target := flag.String("target", "http://localhost:1234", "the real OpenAI-compatible server to forward to")
	dumpDir := flag.String("dump-dir", "", "optional: write each turn's full raw request+response JSON here for offline inspection (e.g. of an empty-content/no-tool-call turn)")
	flag.Parse()

	if *dumpDir != "" {
		if err := os.MkdirAll(*dumpDir, 0o755); err != nil {
			log.Fatalf("dump dir: %v", err)
		}
	}

	targetURL, err := url.Parse(*target)
	if err != nil {
		log.Fatalf("bad target URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			proxy.ServeHTTP(w, r)
			return
		}
		handleChatCompletion(w, r, proxy, *dumpDir)
	})

	log.Printf("context_probe listening on %s, forwarding to %s", *listen, *target)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func handleChatCompletion(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy, dumpDir string) {
	turn := turnCounter.Add(1)
	start := time.Now()

	bodyBytes, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var req struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
	}
	_ = json.Unmarshal(bodyBytes, &req)

	rec := &responseRecorder{ResponseWriter: w, body: &bytes.Buffer{}}
	proxy.ServeHTTP(rec, r)

	var resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string           `json:"content"`
				Reasoning string           `json:"reasoning"`
				ToolCalls []map[string]any `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(rec.body.Bytes(), &resp)

	finishReason, toolCallCount, contentPreview, reasoningPreview := "", 0, "", ""
	if len(resp.Choices) > 0 {
		finishReason = resp.Choices[0].FinishReason
		toolCallCount = len(resp.Choices[0].Message.ToolCalls)
		contentPreview = truncate(resp.Choices[0].Message.Content, 70)
		reasoningPreview = truncate(resp.Choices[0].Message.Reasoning, 70)
	}

	log.Printf("TURN %-3d | req: %2d msgs, %d tools, %6d bytes | resp: prompt=%-6d completion=%-5d total=%-6d tokens | finish=%-11s tool_calls=%d | %6v | content=%q reasoning=%q",
		turn, len(req.Messages), len(req.Tools), len(bodyBytes),
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens,
		finishReason, toolCallCount, time.Since(start).Round(10*time.Millisecond), contentPreview, reasoningPreview)

	if dumpDir != "" {
		_ = os.WriteFile(filepath.Join(dumpDir, "turn_"+padTurn(turn)+"_req.json"), prettyJSON(bodyBytes), 0o644)
		_ = os.WriteFile(filepath.Join(dumpDir, "turn_"+padTurn(turn)+"_resp.json"), prettyJSON(rec.body.Bytes()), 0o644)
	}
}

func padTurn(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 10 {
		s = "0" + s
	}
	return s
}

func prettyJSON(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return pretty
}

type responseRecorder struct {
	http.ResponseWriter
	body *bytes.Buffer
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
