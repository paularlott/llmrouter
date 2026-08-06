package admin

import (
	"net/http"
)

// HandleWatch is the SSE endpoint at GET /admin/api/watch. Connected clients
// receive a real-time stream of LLM request/response events. The connection
// stays open until the client disconnects (closing the overlay in the
// browser). When the last client disconnects, the watcher deactivates and
// the router stops serializing events — zero overhead in normal operation.
func (a *Admin) HandleWatch(w http.ResponseWriter, r *http.Request) {
	if a.watcherSubscribe == nil || a.watcherUnsubscribe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "request watcher not available",
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "streaming not supported",
		})
		return
	}

	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := a.watcherSubscribe()
	defer a.watcherUnsubscribe(ch)

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
