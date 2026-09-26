# ADR: WebRTC Mount Cancellation Token Scoped to Session

## Context & Problem Statement
In ADR `decisions/2026-09-26-video-presentation-mode-and-multi-stream-player.md`, an atomic `mountEpoch` counter was introduced to guard async WebRTC SDP negotiations against rapid SSE flapping. However, because `mountEpoch` was global across the entire `VideoPlayerManager`:
- Any incoming `video.state` SSE event (such as `cast-watcher` reporting a `player_state` transition from `buffering` to `playing`, or a doorbell alert introducing a secondary PiP stream) incremented `this.mountEpoch`.
- While `enterVideoMode` preserved unchanged streams (`id` and `stream_url` match), the in-flight WebRTC negotiation observed `epoch !== this.mountEpoch` after ICE gathering or HTTP SDP exchange, closed `RTCPeerConnection` via `pc.close()`, and aborted.
- The manager retained the dead session without renegotiating, leaving the video slot permanently black until the stream was dismissed and retriggered (Issue #274).

## Decision
1. **Per-Session Lifecycle Token (`web/static/js/video.js`)**:
   - Replaced global `mountEpoch` guards in WebRTC negotiation with a session-scoped cancellation flag (`session.cancelled = false`) and monotonic session sequence identifier (`session.token = ++this.sessionTokenSeq`).
   - In `teardownSession(session)`, set `session.cancelled = true` before closing peer connections, clearing DOM nodes, or unregistering media elements from `AudioManager`.
2. **Session-Guarded Negotiation (`negotiateWebRTC` and `pc.ontrack`)**:
   - In `negotiateWebRTC`, inspect `session.cancelled` across every async boundary (`createOffer`, `setLocalDescription`, ICE gathering timeout, `fetch`, and `res.text()`). If `session.cancelled` is true, close `pc` and abort without logging error warnings.
   - In `pc.ontrack`, verify `session.cancelled` before attaching tracks or setting `video.srcObject`.
3. **Unchanged Stream Preservation**:
   - When incoming `video.state` events update metadata (`player_state`, `title`, etc.) for an unchanged stream ID and URL, `session.cancelled` remains `false`, allowing in-flight SDP negotiation to complete smoothly.
4. **Verification**:
   - Added unit test in `web/test/video.test.js` verifying that `player_state` updates and secondary PiP mounts mid-negotiation do NOT abort or close an in-flight primary WebRTC peer connection.
   - Added unit test verifying that replacing a stream mid-negotiation cancels the stale peer connection and negotiates the new stream.

## Consequences & Alternatives Considered
- **Retaining Global `mountEpoch` with Slot Filtering**: Rejected because slot counters still risk race conditions if streams are rapidly reconfigured across slots. Scoping cancellation directly to the media session object provides hermetic lifecycle coupling with zero cross-stream interference.
- **Positive**: WebRTC streams now withstand normal state synchronization events (like cast player state changes or doorbell alerts) without negotiation drops or permanently black video slots.
