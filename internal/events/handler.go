package events

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// Handler constructs an http.Handler serving GET /api/events for SSE clients.
func Handler(hub *Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. CORS Preflight
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Cache-Control, Last-Event-ID, Content-Type, Accept")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 2. http.Flusher Verification
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// 3. Response Headers
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// 4. Disable write deadline for persistent streaming
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			hub.logger.Debug("failed to disable response write deadline", "error", err)
		}

		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// 5. Subscribe to live stream BEFORE snapshot/replay to eliminate race window (Issue #199)
		ch, unsubscribe := hub.Subscribe(r.Context())
		defer unsubscribe()

		// 6. Initial Hydration vs Replay
		lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
		if lastEventID == "" {
			// Also check query param fallback for non-standard clients
			lastEventID = strings.TrimSpace(r.URL.Query().Get("lastEventId"))
		}

		var (
			initialBatch  []*Event
			seenReplayIDs map[string]struct{}
		)
		if lastEventID != "" {
			if replayed, found := hub.ReplaySince(lastEventID); found {
				initialBatch = replayed
				seenReplayIDs = make(map[string]struct{}, len(replayed)+1)
				seenReplayIDs[lastEventID] = struct{}{}
				for _, evt := range replayed {
					seenReplayIDs[evt.ID] = struct{}{}
				}
			} else {
				// Replay missed/expired: execute full initial state hydration
				initialBatch = hub.BuildHydration()
			}
		} else {
			// Cold boot or no Last-Event-ID: execute full initial state hydration
			initialBatch = hub.BuildHydration()
		}

		// 7. Flush initial batch (hydration or replay)
		for _, evt := range initialBatch {
			if _, err := w.Write(evt.Format()); err != nil {
				return
			}
		}
		flusher.Flush()

		// 8. Keep-alive heartbeat ticker
		ticker := time.NewTicker(hub.cfg.HeartbeatInterval)
		defer ticker.Stop()

		deliverEvent := func(evt *Event) bool {
			if seenReplayIDs != nil {
				if _, seen := seenReplayIDs[evt.ID]; seen {
					return true
				}
				// Reached first new event beyond replayed batch; discard filter map
				seenReplayIDs = nil
			}
			if _, err := w.Write(evt.Format()); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}

		// 9. Drain any events queued during batch preparation
		drainLoop:
		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if !deliverEvent(evt) {
					return
				}
			default:
				break drainLoop
			}
		}

		// 10. Live Stream Loop
		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if !deliverEvent(evt) {
					return
				}
			case t := <-ticker.C:
				if _, err := w.Write(FormatPing(t)); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
