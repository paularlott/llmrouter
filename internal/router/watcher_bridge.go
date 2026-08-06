package router

import (
	"encoding/json"
	"sync"
)

// watcherBridge maps the internal WatchEvent channel to a []byte channel
// that the admin SSE handler consumes. One bridge per connected SSE client.
type watcherBridge struct {
	src  chan WatchEvent
	dst  chan []byte
	done chan struct{}
}

var (
	watcherBridgesMu sync.Mutex
	watcherBridges   = make(map[chan []byte]*watcherBridge)
)

// watcherSubscribe creates a new bridged subscriber. The returned chan []byte
// delivers JSON-encoded WatchEvent payloads. The caller must eventually call
// watcherUnsubscribe to clean up.
func (r *Router) watcherSubscribe() chan []byte {
	src := r.requestWatcher.Subscribe()
	dst := make(chan []byte, 64)
	done := make(chan struct{})

	b := &watcherBridge{src: src, dst: dst, done: done}

	watcherBridgesMu.Lock()
	watcherBridges[dst] = b
	watcherBridgesMu.Unlock()

	// Goroutine: serialize WatchEvent → JSON bytes and forward to dst.
	go func() {
		defer close(dst)
		for {
			select {
			case ev, ok := <-src:
				if !ok {
					return
				}
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				select {
				case dst <- data:
				default:
					// Drop if dst is full (slow SSE consumer).
				}
			case <-done:
				return
			}
		}
	}()

	return dst
}

// watcherUnsubscribe tears down a bridged subscriber: stops the goroutine,
// unsubscribes from the RequestWatcher, and removes the mapping.
func (r *Router) watcherUnsubscribe(dst chan []byte) {
	watcherBridgesMu.Lock()
	b, ok := watcherBridges[dst]
	if ok {
		delete(watcherBridges, dst)
	}
	watcherBridgesMu.Unlock()

	if !ok {
		return
	}

	// Signal the bridge goroutine to stop, then unsubscribe from the
	// underlying RequestWatcher (which closes src).
	close(b.done)
	r.requestWatcher.Unsubscribe(b.src)
}
