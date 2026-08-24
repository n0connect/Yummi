package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is a single chat turn (role: system | user | assistant).
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Thinking carries Ollama's native reasoning-channel output (present for
	// gpt-oss and other "thinking"-capable models even with Options.Think
	// disabled) — see do()'s doc comment on why this is captured and used
	// as a fallback rather than just decoded and discarded.
	Thinking string `json:"thinking,omitempty"`
}

// Options controls inference behaviour for the Ollama request.
// Zero values use Ollama's built-in defaults except where noted.
type Options struct {
	Temperature   float64 // 0.0 = deterministic (default)
	TopP          float64 // nucleus sampling (0 = ollama default)
	TopK          int     // top-k sampling (0 = ollama default)
	RepeatPenalty float64 // repetition penalty (0 = ollama default)
	NumCtx        int     // context window tokens (0 = model default)
	NumGPU        int     // GPU layers (-1 = auto, 0 = CPU only)
	NumThread     int     // CPU threads (0 = ollama auto)
	MMap          *bool   // memory-mapped I/O (nil = ollama default)
	MLock         *bool   // lock model in RAM, prevent swap (nil = default)
	F16KV         *bool   // fp16 KV cache — saves VRAM (nil = default)
	Think         *bool   // Qwen3 thinking mode (nil = model default)
}

// DefaultQwen3Options returns performance-tuned options for qwen3 models.
// Keeps the model resident in RAM, disables thinking mode, and uses a
// 16K context window with fp16 KV cache to balance VRAM and quality.
func DefaultQwen3Options() Options {
	f := false
	t := true
	return Options{
		Temperature:   0.0,
		TopP:          0.9,
		TopK:          40,
		RepeatPenalty: 1.1,
		NumCtx:        16384,
		NumGPU:        99, // push all layers to GPU; Ollama caps at model max
		MMap:          &f, // load fully into RAM instead of mmap
		MLock:         &t, // prevent swap eviction
		F16KV:         &t, // fp16 KV cache
		Think:         &f, // disable Qwen3 extended thinking
	}
}

// Client communicates with the Ollama /api/chat endpoint.
type Client struct {
	baseURL    string
	model      string
	keepAlive  any // Ollama keep_alive: integer -1 = never unload, or string "5m"
	opts       Options
	httpClient *http.Client
	embedModel string // Ollama embedding model tag (e.g. "nomic-embed-text"); empty = Embed disabled
}

// NewClient creates an Ollama client with Qwen3-optimised defaults.
func NewClient(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "qwen3:8b"
	}
	return &Client{
		baseURL:    baseURL,
		model:      model,
		keepAlive:  -1, // integer -1: Ollama never unloads the model from VRAM
		opts:       DefaultQwen3Options(),
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

// WithOptions returns a shallow copy of the client with replaced options.
func (c *Client) WithOptions(opts Options) *Client {
	cp := *c
	cp.opts = opts
	return &cp
}

// WithKeepAlive returns a copy with a custom keep_alive value.
// Pass -1 (integer) to keep the model loaded indefinitely, 0 to unload
// immediately, or a duration string like "5m". Ollama parses integer -1
// as "never unload"; duration strings must include a unit suffix ("5m", "1h").
func (c *Client) WithKeepAlive(v any) *Client {
	cp := *c
	cp.keepAlive = v
	return &cp
}

// Model returns the configured model tag.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// Chat sends a system prompt + single user message to Ollama and returns
// the assistant content as a string.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ollama client not configured")
	}
	msgs := []Message{
		{Role: "system", Content: c.prepareSystem(system)},
		{Role: "user", Content: user},
	}
	return c.do(ctx, msgs)
}

// ChatHistory sends a system prompt followed by a full conversation history.
func (c *Client) ChatHistory(ctx context.Context, system string, history []Message) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ollama client not configured")
	}
	msgs := make([]Message, 0, len(history)+1)
	msgs = append(msgs, Message{Role: "system", Content: c.prepareSystem(system)})
	msgs = append(msgs, history...)
	return c.do(ctx, msgs)
}

// ChatStream is Chat's streaming counterpart: it sends the same request with
// Ollama's "stream": true and invokes onChunk with each incremental piece of
// the assistant message as it arrives, in addition to returning the full,
// accumulated response once the stream completes — so it composes cleanly
// with every existing caller that only cares about the final string (e.g. a
// JSON-parsing consumer like conductor.Engine). onChunk may be nil, in which
// case ChatStream just accumulates silently and behaves like Chat over the
// wire. Requesting Ollama's streamed NDJSON has no bearing on the semantics
// of the accumulated result: it is byte-for-byte the same string Chat would
// have returned for an identical prompt.
func (c *Client) ChatStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ollama client not configured")
	}
	msgs := []Message{
		{Role: "system", Content: c.prepareSystem(system)},
		{Role: "user", Content: user},
	}
	return c.doStream(ctx, msgs, onChunk)
}

// ChatHistoryStream is ChatHistory's streaming counterpart — see ChatStream.
func (c *Client) ChatHistoryStream(ctx context.Context, system string, history []Message, onChunk func(chunk string)) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ollama client not configured")
	}
	msgs := make([]Message, 0, len(history)+1)
	msgs = append(msgs, Message{Role: "system", Content: c.prepareSystem(system)})
	msgs = append(msgs, history...)
	return c.doStream(ctx, msgs, onChunk)
}

// Preload warms the model into VRAM by sending an empty prompt to /api/generate.
// Ollama loads the model without producing tokens; keep_alive="-1" ensures it
// stays resident indefinitely. Call once at startup — before serving requests —
// to guarantee a single cold-start and eliminate per-request load latency.
func (c *Client) Preload(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("ollama client not configured")
	}
	type preloadReq struct {
		Model     string        `json:"model"`
		Prompt    string        `json:"prompt"`
		Stream    bool          `json:"stream"`
		KeepAlive any           `json:"keep_alive"`
		Options   ollamaOptions `json:"options"`
	}
	o := c.opts
	body, err := json.Marshal(preloadReq{
		Model:     c.model,
		Prompt:    "", // empty prompt: load model, generate nothing
		Stream:    false,
		KeepAlive: c.keepAlive,
		Options: ollamaOptions{
			NumCtx:    o.NumCtx,
			NumGPU:    o.NumGPU,
			NumThread: o.NumThread,
			MMap:      o.MMap,
			MLock:     o.MLock,
			F16KV:     o.F16KV,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal preload request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build preload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("preload request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama preload http %d: %s", resp.StatusCode, string(b))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Ping checks whether Ollama is reachable by fetching the model list.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("ollama client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// prepareSystem returns the system prompt unchanged.
// Qwen3 thinking mode is controlled via the top-level "think" field in the
// ollamaRequest — not via a /no_think prefix in the system prompt.
func (c *Client) prepareSystem(system string) string {
	return system
}

// ── internal Ollama request types ────────────────────────────────────────────

type ollamaOptions struct {
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p,omitempty"`
	TopK          int     `json:"top_k,omitempty"`
	RepeatPenalty float64 `json:"repeat_penalty,omitempty"`
	NumCtx        int     `json:"num_ctx,omitempty"`
	NumGPU        int     `json:"num_gpu,omitempty"`
	NumThread     int     `json:"num_thread,omitempty"`
	MMap          *bool   `json:"mmap,omitempty"`
	MLock         *bool   `json:"mlock,omitempty"`
	F16KV         *bool   `json:"f16_kv,omitempty"`
	// Note: "think" does NOT belong here. Ollama ignores it inside options.
	// Qwen3 thinking mode is controlled via the top-level "think" field below.
}

type ollamaRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	Format    string    `json:"format,omitempty"`
	KeepAlive any       `json:"keep_alive,omitempty"`
	// Think controls Qwen3 extended thinking mode. Must be top-level, NOT inside
	// options — Ollama's template renderer reads it here and gates <think> tokens.
	// false = fast responses (no hidden reasoning chain); true = deeper but slower.
	Think   *bool         `json:"think,omitempty"`
	Options ollamaOptions `json:"options"`
}

type ollamaResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
	Error   string  `json:"error,omitempty"`
}

func (c *Client) do(ctx context.Context, messages []Message) (string, error) {
	o := c.opts
	payload := ollamaRequest{
		Model:     c.model,
		Messages:  messages,
		Stream:    false,
		Format:    "json",
		KeepAlive: c.keepAlive,
		Think:     o.Think, // top-level: Ollama gates Qwen3 <think> tokens here
		Options: ollamaOptions{
			Temperature:   o.Temperature,
			TopP:          o.TopP,
			TopK:          o.TopK,
			RepeatPenalty: o.RepeatPenalty,
			NumCtx:        o.NumCtx,
			NumGPU:        o.NumGPU,
			NumThread:     o.NumThread,
			MMap:          o.MMap,
			MLock:         o.MLock,
			F16KV:         o.F16KV,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama http %d: %s", resp.StatusCode, string(b))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("ollama: %s", result.Error)
	}
	content := result.Message.Content
	// Live-observed against gpt-oss served via Ollama's native /api/chat: the
	// model's actual answer sometimes lands entirely in the "thinking"
	// channel with content left empty — the same class of channel-routing
	// inconsistency found (and worked around) in the Eino backend's own
	// handling of this model family, just via a different serving protocol.
	// Falling back to Thinking recovers a usable reply instead of hard-
	// failing a call that, from the model's own perspective, actually
	// succeeded — it just wrote its answer to the field this client wasn't
	// reading.
	if strings.TrimSpace(content) == "" {
		content = result.Message.Thinking
	}
	// An empty/whitespace-only message (in EITHER field) is a model or
	// transport hiccup, not a usable reply. Surface it as an error so callers
	// retry (with backoff) rather than feeding an empty string into a
	// downstream parser.
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("ollama returned an empty message")
	}
	return content, nil
}

// doStream is do's streaming counterpart. Ollama's streamed /api/chat
// responses are newline-delimited JSON objects, each carrying the next
// incremental fragment of message.content, until a final object with
// done:true. onChunk (nilable) is invoked once per fragment as it is
// decoded; the full accumulated content is returned once the stream ends,
// under the same empty-message error policy as do().
func (c *Client) doStream(ctx context.Context, messages []Message, onChunk func(chunk string)) (string, error) {
	o := c.opts
	payload := ollamaRequest{
		Model:     c.model,
		Messages:  messages,
		Stream:    true,
		Format:    "json",
		KeepAlive: c.keepAlive,
		Think:     o.Think, // top-level: Ollama gates Qwen3 <think> tokens here
		Options: ollamaOptions{
			Temperature:   o.Temperature,
			TopP:          o.TopP,
			TopK:          o.TopK,
			RepeatPenalty: o.RepeatPenalty,
			NumCtx:        o.NumCtx,
			NumGPU:        o.NumGPU,
			NumThread:     o.NumThread,
			MMap:          o.MMap,
			MLock:         o.MLock,
			F16KV:         o.F16KV,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama http %d: %s", resp.StatusCode, string(b))
	}

	var full, thinking strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Each NDJSON line carries only one chunk's worth of content, but grow the
	// buffer well past bufio's 64KB default in case a single line is unusually
	// large (e.g. a large tool-call payload some model templates might emit).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk ollamaResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			// A malformed line is a transport hiccup, not fatal to the overall
			// stream — skip it and keep accumulating what follows.
			continue
		}
		if chunk.Error != "" {
			return "", fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Message.Content != "" {
			full.WriteString(chunk.Message.Content)
			if onChunk != nil {
				onChunk(chunk.Message.Content)
			}
		}
		thinking.WriteString(chunk.Message.Thinking)
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read stream: %w", err)
	}

	result := full.String()
	// See do()'s identical fallback: some models (gpt-oss observed live) can
	// stream their real answer entirely on the thinking channel.
	if strings.TrimSpace(result) == "" {
		result = thinking.String()
	}
	if strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("ollama returned an empty message")
	}
	return result, nil
}
