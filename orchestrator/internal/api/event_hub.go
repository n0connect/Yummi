package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/health"
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

const eventSchemaVersion = 1
const defaultEventReplaySize = 512

type EventEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Epoch         string `json:"epoch"`
	Sequence      uint64 `json:"sequence"`
	Type          string `json:"type"`
	Event         string `json:"event,omitempty"`
	Timestamp     int64  `json:"timestamp"`
	SessionID     string `json:"session_id,omitempty"`
	TurnID        string `json:"turn_id,omitempty"`
	Data          any    `json:"data,omitempty"`
}

type EventHello struct {
	SchemaVersion  int    `json:"schema_version"`
	Epoch          string `json:"epoch"`
	LatestSequence uint64 `json:"latest_sequence"`
	ResyncRequired bool   `json:"resync_required"`
}

type eventSubscription struct {
	sessionID string
	ch        chan EventEnvelope
}

// EventHub assigns one process epoch and one monotonic sequence to every
// operator event. Lifecycle events are retained for bounded reconnect replay;
// cosmetic token/output chunks are live-only and remain backed by REST state.
type EventHub struct {
	mu        sync.RWMutex
	epoch     string
	sequence  uint64
	maxReplay int
	replay    []EventEnvelope
	subs      map[int]eventSubscription
	nextSubID int
}

func newEventHubCore(epoch string, maxReplay int) *EventHub {
	if epoch == "" {
		epoch = uuid.NewString()
	}
	if maxReplay <= 0 {
		maxReplay = defaultEventReplaySize
	}
	return &EventHub{epoch: epoch, maxReplay: maxReplay, subs: make(map[int]eventSubscription)}
}

func eventMatchesSession(evt EventEnvelope, sessionID string) bool {
	return sessionID == "" || evt.SessionID == "" || evt.SessionID == sessionID
}

func (h *EventHub) publish(eventType, eventName string, timestamp int64, sessionID, turnID string, data any, replayable bool) EventEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	evt := EventEnvelope{
		SchemaVersion: eventSchemaVersion, Epoch: h.epoch, Sequence: h.sequence,
		Type: eventType, Event: eventName, Timestamp: timestamp,
		SessionID: sessionID, TurnID: turnID, Data: data,
	}
	if replayable {
		h.replay = append(h.replay, evt)
		if len(h.replay) > h.maxReplay {
			h.replay = append([]EventEnvelope(nil), h.replay[len(h.replay)-h.maxReplay:]...)
		}
	}
	for id, sub := range h.subs {
		if !eventMatchesSession(evt, sub.sessionID) {
			continue
		}
		select {
		case sub.ch <- evt:
		default:
			// A full subscriber buffer must never turn a lifecycle transition
			// into a silent hole. Closing the stream forces the client through
			// its replay cursor (or an explicit REST resync if the ring moved).
			close(sub.ch)
			delete(h.subs, id)
		}
	}
	return evt
}

// subscribe captures replay and registers the live sink under one lock, so no
// event can land in the gap between the two operations.
func (h *EventHub) subscribe(sessionID, clientEpoch string, after uint64, buffer int) (EventHello, []EventEnvelope, int, <-chan EventEnvelope) {
	if buffer <= 0 {
		buffer = 64
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	hello := EventHello{SchemaVersion: eventSchemaVersion, Epoch: h.epoch, LatestSequence: h.sequence}
	replay := make([]EventEnvelope, 0)
	if clientEpoch != "" {
		if clientEpoch != h.epoch {
			hello.ResyncRequired = true
		} else {
			// The ring is global, so use its global lower bound. Looking only
			// for this session's oldest retained event is unsafe: every event
			// for a quiet session may have been evicted by other sessions, in
			// which case there is no session-local element left to prove the
			// gap. A conservative REST resync is preferable to a silent hole.
			var oldest uint64
			if len(h.replay) > 0 {
				oldest = h.replay[0].Sequence
			}
			if oldest > 0 && after+1 < oldest {
				hello.ResyncRequired = true
			} else {
				for _, evt := range h.replay {
					if evt.Sequence > after && eventMatchesSession(evt, sessionID) {
						replay = append(replay, evt)
					}
				}
			}
		}
	}

	h.nextSubID++
	id := h.nextSubID
	ch := make(chan EventEnvelope, buffer)
	h.subs[id] = eventSubscription{sessionID: sessionID, ch: ch}
	return hello, replay, id, ch
}

func (h *EventHub) unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sub, ok := h.subs[id]; ok {
		close(sub.ch)
		delete(h.subs, id)
	}
}

func NewEventHub(executions *execution.Store, tasks *agent.Store, agg *health.Aggregator, curatorStore *skillcurator.Store, assistant *conductor.Engine) *EventHub {
	h := newEventHubCore("", defaultEventReplaySize)
	if executions != nil {
		_, ch, err := executions.Subscribe(128)
		if err == nil {
			go func() {
				for evt := range ch {
					replayable := evt.Type != "execution.output"
					h.publish("execution.status", evt.Type, evt.Timestamp, evt.Record.SessionID, "", evt.Record, replayable)
				}
			}()
		}
	}
	if tasks != nil {
		_, taskCh, err := tasks.Subscribe(128)
		if err == nil {
			go func() {
				for evt := range taskCh {
					h.publish("task.status", evt.Type, evt.Timestamp, evt.Task.SessionID, "", evt.Task, true)
				}
			}()
		}
		_, activityCh, err := tasks.SubscribeActivity(256)
		if err == nil {
			go func() {
				for env := range activityCh {
					h.publish("agent.activity", "", env.Event.Timestamp, env.Event.SessionID, "", env.Event, true)
				}
			}()
		}
	}
	if agg != nil {
		_, ch, err := agg.SubscribeGateway(64)
		if err == nil {
			go func() {
				for evt := range ch {
					h.publish("gateway.status", evt.Type, evt.Timestamp, "", "", evt.Rules, true)
				}
			}()
		}
	}
	if curatorStore != nil {
		_, ch, err := curatorStore.Subscribe(64)
		if err == nil {
			go func() {
				for evt := range ch {
					h.publish("skill.status", evt.Type, evt.Timestamp, evt.Skill.SessionID, "", evt.Skill, true)
				}
			}()
		}
	}
	if assistant != nil {
		_, ch, err := assistant.SubscribeStream(128)
		if err == nil {
			go func() {
				for evt := range ch {
					name := "chat.delta"
					if evt.Done {
						name = "chat.done"
					}
					h.publish("chat.stream", name, time.Now().Unix(), evt.SessionID, evt.TurnID, evt, false)
				}
			}()
		}
	}
	return h
}

func HandleEventHubWS(h *EventHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h == nil {
			http.Error(w, "event hub unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		sessionID := r.URL.Query().Get("session_id")
		if sessionID != "" {
			sessionID = normalizeSessionID(sessionID)
		}
		after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		hello, replay, id, live := h.subscribe(sessionID, r.URL.Query().Get("epoch"), after, 128)
		defer h.unsubscribe(id)
		if err := conn.WriteJSON(map[string]any{"type": "control.hello", "data": hello}); err != nil {
			return
		}
		if !hello.ResyncRequired {
			for _, evt := range replay {
				if err := conn.WriteJSON(evt); err != nil {
					return
				}
			}
		}

		pingTicker := time.NewTicker(20 * time.Second)
		defer pingTicker.Stop()
		for {
			select {
			case evt, ok := <-live:
				if !ok || conn.WriteJSON(evt) != nil {
					return
				}
			case <-pingTicker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(2*time.Second)); err != nil {
					return
				}
			}
		}
	}
}

// HandleEventsWS preserves the existing construction surface used by tests
// and callers while sharing one hub across every connection to this handler.
func HandleEventsWS(executions *execution.Store, tasks *agent.Store, agg *health.Aggregator, curatorStore *skillcurator.Store, assistant *conductor.Engine) http.HandlerFunc {
	if executions == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "execution store unavailable", http.StatusServiceUnavailable)
		}
	}
	return HandleEventHubWS(NewEventHub(executions, tasks, agg, curatorStore, assistant))
}
