package router

import (
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// RequestWatcher provides real-time streaming of LLM request/response data
// to connected admin clients via SSE. When no clients are connected, the
// watcher has zero overhead (a single atomic load on the hot path).
type RequestWatcher struct {
	subscribers map[chan WatchEvent]struct{}
	mu          sync.RWMutex
	active      atomic.Int32  // fast-path check: >0 means someone is listening
	reqSeq      atomic.Uint64 // monotonic request ID generator
}

// WatchEvent is one event streamed to watchers.
//
// Every event carries a RequestID that ties it to a logical request group.
// The frontend uses this to nest responses/chunks inside their parent request.
//
// Direction values:
//   - "request"      — the outbound LLM request (one per group, always first)
//   - "response"     — a non-streaming response (one per group)
//   - "stream_chunk" — one streaming chunk (many per group)
//   - "stream_done"  — signals end of streaming (one per group)
//   - "error"        — an error occurred
type WatchEvent struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
	Direction string `json:"direction"`
	Endpoint  string `json:"endpoint"` // e.g. "chat/completions", "messages", "embeddings", "ollama/chat"
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Data      any    `json:"data"`
}

// NewRequestWatcher creates a new watcher with no subscribers.
func NewRequestWatcher() *RequestWatcher {
	return &RequestWatcher{
		subscribers: make(map[chan WatchEvent]struct{}),
	}
}

// Active returns true if at least one client is watching. Hot-path callers
// use this to skip serialization/broadcast entirely when nobody cares.
func (w *RequestWatcher) Active() bool {
	if w == nil {
		return false
	}
	return w.active.Load() > 0
}

// NewRequestID allocates a new monotonic request ID for grouping events.
func (w *RequestWatcher) NewRequestID() string {
	if w == nil {
		return ""
	}
	return strconv.FormatUint(w.reqSeq.Add(1), 10)
}

// Subscribe adds a new watcher client. Returns a channel that receives
// events. The caller must call Unsubscribe when done.
func (w *RequestWatcher) Subscribe() chan WatchEvent {
	ch := make(chan WatchEvent, 128)
	w.mu.Lock()
	w.subscribers[ch] = struct{}{}
	w.active.Add(1)
	w.mu.Unlock()
	return ch
}

// Unsubscribe removes a watcher client and closes its channel.
func (w *RequestWatcher) Unsubscribe(ch chan WatchEvent) {
	w.mu.Lock()
	if _, ok := w.subscribers[ch]; ok {
		delete(w.subscribers, ch)
		w.active.Add(-1)
		close(ch)
	}
	w.mu.Unlock()
}

// Emit broadcasts an event to all connected watchers. Non-blocking: if a
// subscriber's channel is full, the event is dropped for that subscriber
// (slow consumers don't block the router).
func (w *RequestWatcher) Emit(ev WatchEvent) {
	if w == nil || !w.Active() {
		return
	}
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	w.mu.RLock()
	for ch := range w.subscribers {
		select {
		case ch <- ev:
		default:
			// Drop event for slow subscriber.
		}
	}
	w.mu.RUnlock()
}

// EmitRequest emits the opening "request" event for a new group.
func (w *RequestWatcher) EmitRequest(requestID, endpoint, provider, model string, payload any) {
	if w == nil || !w.Active() {
		return
	}
	w.Emit(WatchEvent{
		RequestID: requestID,
		Direction: "request",
		Endpoint:  endpoint,
		Provider:  provider,
		Model:     model,
		Data:      payload,
	})
}

// EmitResponse emits a non-streaming response event.
func (w *RequestWatcher) EmitResponse(requestID, endpoint, provider, model string, payload any) {
	if w == nil || !w.Active() {
		return
	}
	w.Emit(WatchEvent{
		RequestID: requestID,
		Direction: "response",
		Endpoint:  endpoint,
		Provider:  provider,
		Model:     model,
		Data:      payload,
	})
}

// EmitStreamChunk emits a single streaming chunk.
func (w *RequestWatcher) EmitStreamChunk(requestID, endpoint, provider, model string, chunk any) {
	if w == nil || !w.Active() {
		return
	}
	w.Emit(WatchEvent{
		RequestID: requestID,
		Direction: "stream_chunk",
		Endpoint:  endpoint,
		Provider:  provider,
		Model:     model,
		Data:      chunk,
	})
}

// EmitStreamDone signals the end of a streaming response.
func (w *RequestWatcher) EmitStreamDone(requestID, endpoint, provider, model string) {
	if w == nil || !w.Active() {
		return
	}
	w.Emit(WatchEvent{
		RequestID: requestID,
		Direction: "stream_done",
		Endpoint:  endpoint,
		Provider:  provider,
		Model:     model,
	})
}

// EmitError emits an error event.
func (w *RequestWatcher) EmitError(requestID, endpoint, provider, model string, err error) {
	if w == nil || !w.Active() {
		return
	}
	w.Emit(WatchEvent{
		RequestID: requestID,
		Direction: "error",
		Endpoint:  endpoint,
		Provider:  provider,
		Model:     model,
		Data:      map[string]string{"error": err.Error()},
	})
}

// MarshalEvent serializes a WatchEvent to JSON bytes for SSE.
func MarshalEvent(ev WatchEvent) []byte {
	data, _ := json.Marshal(ev)
	return data
}
