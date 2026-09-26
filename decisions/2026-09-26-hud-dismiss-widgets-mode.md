# ADR: Touch HUD Dismiss Unmounts Video Mode Returning to Widgets Mode

## Context & Problem Statement
In SPEC-004 §2 ("Touch HUD & Transport Controls"), the Dismiss ('X') button is specified as:
`- **Dismiss ('X') Button**: Unmounts video mode immediately and returns to widgets mode.`

However, in PR #275 (Chunk 4.3B), `web/static/js/video_hud.js` dispatched `POST /api/video/dismiss` with only `this.activeStream.id`. When a PiP stream was docked alongside a primary stream (e.g. a doorbell alert arriving during an active Chromecast session), dismissing the primary stream triggered the priority stack's alert promotion logic (`coordinator.go` lines 249-265), promoting the doorbell to fullscreen unmuted video. The display remained locked in `video` mode rather than returning to `widgets` mode (Issue #277).

## Decision
1. **Multi-Stream Dismissal in Touch HUD (`web/static/js/video_hud.js`)**:
   - Updated `handleDismiss()` to inspect both `serverPrimary` and `serverPip` (respecting swapped presentation states).
   - When multiple streams are present (both primary and PiP are active), `handleDismiss()` dispatches `POST /api/video/dismiss` with `{"id": "all"}` so video mode unmounts immediately and returns to `widgets` mode per SPEC-004 §2.
   - When only one stream is active, `handleDismiss()` continues to dispatch `POST /api/video/dismiss` with that stream's specific ID.
2. **Atomic All-Stream Dismissal (`internal/video/coordinator.go`)**:
   - Added support for `id: "all"` and `id: "*"` in `Coordinator.Dismiss(id string)` and exposed `Coordinator.DismissAll()`.
   - Clears all running stream timers, clears both primary and PiP streams, transitions mode to `ModeWidgets`, and broadcasts the updated `video.state` SSE event atomically.
3. **API & Specification Alignment**:
   - Updated `api/openapi.yaml` and `specs/004-video-cast-ingest.md` to document `id: "all"` option for `POST /api/video/dismiss`.
   - Verified that upstream disconnects targeting specific stream IDs (e.g. `cast-watcher` posting for `chromecast`) continue to promote active PiP alerts per SPEC-004 §Transition State Machine.

## Consequences & Alternatives Considered
- **Amending SPEC-004 to Promote PiP on HUD 'X' Click**: Rejected because kiosk users tapping 'X' on a smart display dashboard expect to exit media playback and return to their ambient dashboard calendar and widgets, not promote background camera alerts to fullscreen.
- **Positive**: Consistent appliance-grade UX where the on-screen 'X' button always returns the kiosk to widgets mode, regardless of whether secondary PiP alerts or stream swaps are active.
