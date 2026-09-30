package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChatWithHistoryTurnRejectsConcurrentTurnAndCorrelatesStream(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"intent\":\"explain\",\"message\":\"ok\",\"confidence\":0.9}"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}` + "\n"))
	}))
	t.Cleanup(srv.Close)

	e, err := NewEngine("", srv.URL, "test-model")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	_, stream, err := e.SubscribeStream(8)
	if err != nil {
		t.Fatalf("SubscribeStream: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, runErr := e.ChatWithHistoryTurn(context.Background(), "sess-1", "turn-1", "first")
		firstDone <- runErr
	}()
	<-started

	if _, err := e.ChatWithHistoryTurn(context.Background(), "sess-1", "turn-2", "second"); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("second same-session turn error = %v, want ErrTurnInProgress", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first turn: %v", err)
	}

	for {
		evt := <-stream
		if evt.TurnID != "turn-1" {
			t.Fatalf("stream turn_id = %q, want turn-1", evt.TurnID)
		}
		if evt.Done {
			break
		}
	}
}

// TestStreamBrokerSubscribePublishUnsubscribe exercises the broker directly:
// a subscriber receives published events, and stops receiving (channel
// closed) after Unsubscribe.
func TestStreamBrokerSubscribePublishUnsubscribe(t *testing.T) {
	var b streamBroker
	id, ch := b.subscribe(4)

	b.publish(StreamEvent{SessionID: "s1", Delta: "hel"})
	b.publish(StreamEvent{SessionID: "s1", Delta: "lo", Done: true})

	first := <-ch
	if first.Delta != "hel" || first.Done {
		t.Fatalf("unexpected first event: %+v", first)
	}
	second := <-ch
	if second.Delta != "lo" || !second.Done {
		t.Fatalf("unexpected second event: %+v", second)
	}

	b.unsubscribe(id)
	if _, ok := <-ch; ok {
		t.Fatal("expected channel to be closed after unsubscribe")
	}
}

// TestStreamBrokerPublishDropsOnFullChannelInsteadOfBlocking proves the
// broker never blocks a chat call on a slow subscriber — publish is
// non-blocking, matching health.Aggregator.diffAndPublishRules's pattern.
func TestStreamBrokerPublishDropsOnFullChannelInsteadOfBlocking(t *testing.T) {
	var b streamBroker
	_, ch := b.subscribe(1) // buffer of exactly 1

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.publish(StreamEvent{Delta: "one"})
		b.publish(StreamEvent{Delta: "two"}) // channel already full — must not block
		b.publish(StreamEvent{Delta: "three"})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full subscriber channel")
	}

	evt := <-ch
	if evt.Delta != "one" {
		t.Fatalf("expected the first published event to survive, got %+v", evt)
	}
}

// TestEngineSubscribeStreamNilSafe mirrors the package's other nil-engine
// tests: calling SubscribeStream/UnsubscribeStream/publishStream on a nil
// *Engine must never panic.
func TestEngineSubscribeStreamNilSafe(t *testing.T) {
	var e *Engine
	id, ch, err := e.SubscribeStream(4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 0 {
		t.Fatalf("expected id 0 for nil engine, got %d", id)
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected an already-closed channel from a nil engine")
	}
	e.UnsubscribeStream(1)                          // must not panic
	e.publishStream("s1", "turn-1", "delta", false) // must not panic
}

// newNDJSONOllamaServer is a real httptest.Server (not a mock) answering
// /api/chat with a streamed NDJSON response, one line per element of chunks
// plus a final {done:true} line — mirrors llm.newNDJSONOllamaServer's shape,
// duplicated here (rather than exported) to keep the llm package's test
// helpers private to that package.
func newNDJSONOllamaServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			line, _ := json.Marshal(map[string]any{
				"message": map[string]string{"role": "assistant", "content": c},
			})
			_, _ = w.Write(append(line, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}` + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestChatWithHistoryPublishesStreamDeltas proves the actual wiring asked
// for by this feature: ChatWithHistory's internal LLM call streams, and the
// extracted "message" field text is published on the session's chat.stream
// channel as it becomes available, followed by a terminal done=true event —
// all while the returned ChatResponse is parsed from the fully-accumulated
// RAW text exactly as before. The model's raw output is the WHOLE
// {"intent":...,"message":"...",...} JSON object, not prose — streaming it
// verbatim let the operator's live "typing" preview briefly show raw JSON
// syntax before the REST reply replaced it with the parsed message (see
// extractMessagePreview). The message VALUE is deliberately split across
// both raw chunks here (chunk 1 ends mid-string) so this also proves genuine
// incremental delivery of the message text itself, not just "reveal it all
// once the field closes".
func TestChatWithHistoryPublishesStreamDeltas(t *testing.T) {
	srv := newNDJSONOllamaServer(t, `{"intent":"explain","message":"hi `, `there","confidence":0.9}`)

	e, err := NewEngine("", srv.URL, "test-model")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, ch, err := e.SubscribeStream(8)
	if err != nil {
		t.Fatalf("SubscribeStream: %v", err)
	}

	resp := e.ChatWithHistory(context.Background(), "sess-1", "hello")
	if resp.Message != "hi there" {
		t.Fatalf("expected parsed ChatResponse.Message %q, got %+v", "hi there", resp)
	}

	var mu sync.Mutex
	var deltas []string
	doneSeen := false
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case evt := <-ch:
			mu.Lock()
			if evt.Done {
				doneSeen = true
				mu.Unlock()
				break collect
			}
			deltas = append(deltas, evt.Delta)
			mu.Unlock()
		case <-deadline:
			break collect
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if !doneSeen {
		t.Fatal("expected a terminal done=true StreamEvent")
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 delta events, got %d: %+v", len(deltas), deltas)
	}
	for _, d := range deltas {
		if strings.ContainsAny(d, "{}") {
			t.Fatalf("delta leaked raw JSON structure: %+v", deltas)
		}
	}
	got := strings.Join(deltas, "")
	want := "hi there"
	if got != want {
		t.Fatalf("streamed deltas did not reassemble to the extracted message: got %q want %q", got, want)
	}
}

// TestChatPublishesNothingWithoutASession proves the session-less Chat()
// path is a safe no-op on the stream broker: it still uses the streaming
// HTTP primitive internally (exercised for real, not skipped), but never
// scopes an event to an empty session id, so a subscriber sees nothing from
// it — there is no WS audience to address such an event to anyway (see
// publishStream's empty-sessionID guard in stream.go).
func TestChatPublishesNothingWithoutASession(t *testing.T) {
	srv := newNDJSONOllamaServer(t, `{"intent":"explain","message":"ok","confidence":0.9}`)

	e, err := NewEngine("", srv.URL, "test-model")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, ch, err := e.SubscribeStream(8)
	if err != nil {
		t.Fatalf("SubscribeStream: %v", err)
	}

	resp := e.Chat(context.Background(), "hello")
	if resp.Message != "ok" {
		t.Fatalf("expected parsed message 'ok', got %+v", resp)
	}

	select {
	case evt := <-ch:
		t.Fatalf("expected no stream event for the session-less Chat() path, got %+v", evt)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}
}
