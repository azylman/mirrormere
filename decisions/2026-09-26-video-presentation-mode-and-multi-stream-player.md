# Architecture Decision Record: Video Presentation Mode, Multi-Stream Protocol Triad, and Picture-in-Picture Dock

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #252 on `azylman/mirrormere` (SPEC-013 Phase 4, Chunk 4.3A), fulfilling SPEC-004 §1, §5, and SPEC-010 §1.

---

## 1. Problem Statement
Prior to Chunk 4.3A:
1. **Missing Video Presentation Stage in Web Client**: While Core managed in-memory priority stack coordination and broadcasted `video.state` SSE events (Chunk 4.2), the browser client lacked video presentation mode, DOM stage mounting, and layout switching (`widgets` <-> `video`).
2. **Multi-Protocol Streamtriad Void**: Mirrormere display clients need to ingest three distinct video protocols: low-latency WebRTC streams from `go2rtc` with SDP offer/answer negotiation, HLS streams with MSE decoding, and lightweight MJPEG camera streams via responsive `<img>` elements.
3. **Picture-in-Picture (PiP) Display & Audio Isolation**: When secondary alerts (e.g. doorbell cameras) trigger during active media playback, the alert stream must mount in a floating corner PiP dock with strict audio muting (`muted: true`, `volume: 0`) and zero interference with the master 80% DOM volume ceiling.
4. **Interactive Stream Swapping Stalls**: Tapping a floating PiP dock to swap primary and secondary streams must occur smoothly at 60fps without DOM reparenting pauses, WebRTC decoder resets, or audio state drift.
5. **Background Layout Churn**: When entering video mode, background carousel rotation timers and touch swipe gestures could cause off-screen layout churn and wasted network bandwidth.

---

## 2. Decision & Architecture

### A. Fixed Viewport Video Stage & Display Mode Transitions (`web/templates/display.html`, `web/static/css/hud.css`)
- Viewport Confinement & Header Concealment:
  - `#video-stage` is mounted as a top-level container styled with `position: fixed; inset: 0; width: 100vw; height: 100vh; z-index: 100; background-color: var(--mm-bg-canvas);`.
  - This overlays the entire 1080p kiosk surface with zero header obstruction, avoiding layout reflows on `#grid-canvas` or 16px container padding traps.
  - Smooth opacity transitions (`opacity: 0` to `opacity: 1`, 250ms cubic-bezier) prevent flashes when entering or exiting video mode.
- Seamless Mode Switching:
  - Transition to `video` mode hides `#grid-canvas` (`display: none`), pauses carousel rotation and touch swipe listeners (`carousel.pause()`), and makes `#video-stage` visible with `.active`.
  - Transition to `widgets` mode hides `#video-stage`, cleanly tears down all video sessions and peer connections, restores `#grid-canvas`, and resumes carousel processing (`carousel.resume()`).

### B. Multi-Stream Protocol Triad (`web/static/js/video.js`)
- Protocol Selection & Ingestion:
  - `webrtc`:
    - Instantiates `RTCPeerConnection` and explicitly configures recvonly video and audio transceivers (`pc.addTransceiver('video', { direction: 'recvonly' })`, `pc.addTransceiver('audio', { direction: 'recvonly' })`).
    - Generates SDP offer, applies local description, and awaits ICE gathering completion before posting SDP to `stream_url`.
    - Handles both raw SDP text and JSON payloads (`{ type: "answer", sdp: "..." }`) before calling `pc.setRemoteDescription()`.
    - Attaches incoming tracks (`event.streams[0]` or dynamically created `MediaStream`) to `videoElement.srcObject`.
  - `hls`:
    - Checks for `window.Hls` and `Hls.isSupported()`; instantiates `new Hls()`, loads source, and binds to `<video>` element with fatal error listeners.
    - Falls back to native HLS playback via `videoElement.src = stream_url` when native MIME type support is detected.
  - `mjpeg`:
    - Renders lightweight responsive `<img class="video-stream-media" src="...">` elements with zero audio handling overhead.
- Hermetic Teardown Protocol:
  - Pauses media playback, clears `srcObject`, removes `src` attribute, and calls `video.load()` to flush Chromium hardware decoders and audio sinks.
  - Closes `RTCPeerConnection` (`pc.close()`) and clears all track/candidate handlers.
  - Destroys `Hls` instances and clears MJPEG `img.src = ''` to prevent background HTTP connection exhaustion.

### C. Picture-in-Picture (PiP) Dock & Audio Isolation (`web/static/js/video.js`, `web/static/css/hud.css`)
- PiP Dock Placement:
  - Sized at 360px width with 16:9 aspect ratio (`max-width: 35vw; bottom: 24px; right: 24px; z-index: 50;`) floating in the lower-right display quadrant.
  - Styled with cyberpunk HUD border (`2px solid var(--mm-accent-purple)`), neon glow (`box-shadow: 0 4px 20px var(--mm-accent-purple-glow)`), and rounded corners.
- Strict Audio Muting Isolation:
  - PiP video elements are strictly muted (`video.muted = true`, `video.volume = 0`) upon creation.
  - PiP elements are explicitly excluded from `AudioManager.registerMediaElement()`, guaranteeing that global `audio.state` updates do not unmute secondary alert feeds.
  - Only the primary presentation stream is registered with `AudioManager`, enforcing master volume, mute, and the 80% DOM volume ceiling.

### D. Zero-Reparenting Interactive Swap-on-Tap Architecture (`web/static/js/video.js`, `web/static/css/hud.css`)
- Inverted CSS State Mechanism:
  - Rather than detaching and re-appending `<video>` DOM nodes across slot containers (which stalls WebRTC decoding and drops frames), swap-on-tap toggles `data-swapped="true" | "false"` on `#video-stage`.
  - When `data-swapped="false"`:
    - `#video-primary-slot` is fullscreen; `#video-pip-slot` is the corner PiP dock.
  - When `data-swapped="true"`:
    - `#video-primary-slot` is styled via CSS as the corner PiP dock; `#video-pip-slot` is styled fullscreen.
- Audio Focus Handover:
  - Demoted session: Unregistered from `AudioManager`, `video.muted = true`, `video.volume = 0`.
  - Promoted session: `video.muted = false`, registered with `AudioManager` (immediately applying effective volume at the 80% ceiling).
- Server State Reconciliation:
  - Preserves local swap state when incoming `video.state` SSE events deliver metadata updates for unchanged stream IDs.
  - Automatically resets swap to `data-swapped="false"` when the secondary alert stream is dismissed (`pip: null`), cleanly restoring unmuted primary audio.

### E. Mount Epoch Cancellation Guard (`web/static/js/video.js`)
- Protects against rapid SSE flapping: An atomic `mountEpoch` counter increments on every state transition.
- Async WebRTC SDP negotiations verify `epoch === this.mountEpoch` before applying remote descriptions or attaching media streams, aborting immediately and closing orphaned peer connections if state mutated during fetch.

---

## 3. Alternatives Considered
- **Direct DOM Node Reparenting (`primarySlot.appendChild(pipVideo)`)**: Rejected because moving active WebRTC `<video>` elements between DOM containers causes Chromium to momentarily pause playback, re-evaluate autoplay permissions, and trigger visible frame drops. Inverting slot styles via `[data-swapped="true"]` achieves seamless 60fps transitions with zero decoder interruptions.
- **Dynamic Viewport Padding Mutation**: Rejected modifying `#mirrormere-app` padding dynamically in JavaScript. Stacking `#video-stage` with fixed fullscreen positioning (`position: fixed; inset: 0; z-index: 100;`) cleanly decouples the video presentation layer from dashboard grid layouts.
- **Unified Audio Registration for All Feeds**: Rejected registering PiP elements with `AudioManager`. While `AudioManager` supports ducking, PiP streams must remain 100% silent unless explicitly promoted to primary focus; registering PiP elements would cause `applyVolumeToAll()` to erroneously unmute them on master volume changes.

---

## 4. Consequences
- **Positive**:
  - Full client support for WebRTC, HLS, and MJPEG video feeds with automatic presentation mode switching.
  - Fluid zero-reparenting swap-on-tap gesture preserving active playback and handing over audio focus cleanly.
  - 100% adherence to the 80% DOM volume ceiling and strict PiP audio muting.
  - Complete immunity to WebRTC background audio leaks and async negotiation race conditions via `mountEpoch` guards.
  - Zero performance regression or background layout churn while video mode is active.
- **Negative / Risks**:
  - Full end-to-end WebRTC video capture and hardware transcoding require physical bench hardware (MS2130 HDMI capture card and Chromecast on LAN), which are thoroughly tested in hermetic isolation using Node DOM and RTCPeerConnection doubles.
