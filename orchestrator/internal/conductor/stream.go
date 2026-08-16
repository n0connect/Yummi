package conductor

import "sync"

// StreamEvent carries an incremental raw token (or the terminal done signal)
// produced while Engine.Chat/ChatWithHistory streams a response from Ollama.
// It is broadcast over /ws/events as a "chat.stream" event (see
// api.HandleEventsWS) so the operator's chat box can render tokens as they
// arrive instead of sitting on a blank spinner for however long the model
// takes to finish. Purely a display aid: the backend still buffers the full
// response internally and parses ChatResponse from it exactly as before —
// this event never substitutes for, or changes the timing of, that REST
// reply.
type StreamEvent struct {
	SessionID string `json:"session_id"`
	Delta     string `json:"delta,omitempty"`
	Done      bool   `json:"done"`
}

// streamBroker fans StreamEvents out to subscribers. Mirrors
// health.Aggregator's gwSubs pattern (RWMutex-guarded subscriber map,
// non-blocking publish). A slow or gone subscriber never blocks a chat call:
// a full channel just drops the delta, which is safe here because the
// stream is cosmetic — perceived progress only. The authoritative reply
// still arrives over the ordinary REST response regardless of what this
// broker delivers.
type streamBroker struct {
	mu   sync.RWMutex
	subs map[int]chan StreamEvent
	next int
}

func (b *streamBroker) subscribe(buffer int) (int, <-chan StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = make(map[int]chan StreamEvent)
	}
	b.next++
	id := b.next
	ch := make(chan StreamEvent, buffer)
	b.subs[id] = ch
	return id, ch
}

func (b *streamBroker) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		close(ch)
		delete(b.subs, id)
	}
}

func (b *streamBroker) publish(evt StreamEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default: // slow subscriber — drop rather than block the chat call
		}
	}
}

// SubscribeStream registers a new subscriber for "chat.stream" display-token
// events. Mirrors health.Aggregator.SubscribeGateway's shape so
// api.HandleEventsWS can wire it in the same way as the other event
// channels sharing that websocket. Safe on a nil engine (e.g. LLM
// unconfigured) — returns an already-closed channel, matching this
// package's nil-safe degradation pattern elsewhere.
func (e *Engine) SubscribeStream(buffer int) (int, <-chan StreamEvent, error) {
	if e == nil {
		ch := make(chan StreamEvent)
		close(ch)
		return 0, ch, nil
	}
	id, ch := e.stream.subscribe(buffer)
	return id, ch, nil
}

// UnsubscribeStream removes a subscriber and closes its channel. Safe on a
// nil engine or an id that was already removed.
func (e *Engine) UnsubscribeStream(id int) {
	if e == nil {
		return
	}
	e.stream.unsubscribe(id)
}

// publishStream broadcasts a delta (or the terminal done signal) scoped to
// sessionID. A no-op when sessionID is empty — the session-less Chat() path
// has no WS audience to scope the event to — or when the engine is nil.
func (e *Engine) publishStream(sessionID, delta string, done bool) {
	if e == nil || sessionID == "" {
		return
	}
	e.stream.publish(StreamEvent{SessionID: sessionID, Delta: delta, Done: done})
}
