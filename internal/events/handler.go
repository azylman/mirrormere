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

		// 5. Initial Hydration vs Replay
		lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
		if lastEventID == "" {
			// Also check query param fallback for non-standard clients
			lastEventID = strings.TrimSpace(r.URL.Query().Get("lastEventId"))
		}

		var initialBatch []*Event
		if lastEventID != "" {
			if replayed, found := hub.ReplaySince(lastEventID); found {
				initialBatch = replayed
			} else {
				// Replay missed/expired: execute full initial state hydration
				initialBatch = hub.BuildHydration()
			}
		} else {
			// Cold boot or no Last-Event-ID: execute full initial state hydration
			initialBatch = hub.BuildHydration()
		}

		for _, evt := range initialBatch {
			if _, err := w.Write(evt.Format()); err != nil {
				return
			}
		}
		flusher.Flush()

		// 6. Subscribe to live stream
		ch, unsubscribe := hub.Subscribe(r.Context())
		defer unsubscribe()

		// 7. Keep-alive heartbeat ticker
		ticker := time.NewTicker(hub.cfg.HeartbeatInterval)
		defer ticker.Stop()

		// 8. Stream Loop
		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if _, err := w.Write(evt.Format()); err != nil {
					return
				}
				flusher.Flush()
			case t := <-ticker.C:
				if _, err := w.Write(FormatPing(t)); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
