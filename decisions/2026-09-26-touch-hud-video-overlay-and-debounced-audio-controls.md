# Architecture Decision Record: Touch HUD Video Overlay & Debounced Audio Controls

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #253 on `azylman/mirrormere` (SPEC-013 Phase 4, Chunk 4.3B), fulfilling SPEC-004 §5, SPEC-010 §1, and SPEC-010 §4.

---

## 1. Problem Statement
Prior to Chunk 4.3B:
1. **Missing On-Screen Transport & Volume Controls During Video Playback**: While Chunk 4.3A introduced full-screen video presentation and multi-stream PiP, users on the touch kiosk lacked on-screen controls to dismiss video streams, toggle playback on controllable sources (e.g. Chromecast), or adjust system volume without navigating away from the stream.
2. **Kiosk Inactivity Lifecycle Void**: Controls displayed permanently over live video degrade viewing ergonomics. The interface requires an ambient auto-fade lifecycle that conceals controls after 5 seconds of inactivity while waking instantly on any surface tap.
3. **Appliance-Grade Touch Target Deficit**: Touch kiosk monitors require large, tactile touch targets (>= 48x48px hit areas), text selection suppression, and transparent tap highlight styling to prevent browser UI artifacts during finger drags and rapid tapping.
4. **Network Saturation from Continuous Slider Gestures**: Continuous dragging of touch volume sliders can fire 60 input events per second. Unthrottled network requests would overwhelm the Go HTTP server and saturate local WiFi, while naive debouncing introduces visual lag or drops final release values.
5. **Rubber-Banding Race Conditions from SSE Echoes**: When volume changes are mutated, incoming `audio.state` SSE events arrive asynchronously. If the slider unconditionally accepts incoming SSE updates while a user is dragging or immediately after release, the slider thumb visually snaps back to stale positions (rubber-banding).
6. **PiP Gesture Collisions**: An overlay spanning the viewport can accidentally intercept touch taps intended for the corner PiP dock, blocking the zero-reparenting swap-on-tap gesture.

---

## 2. Decision & Architecture

### A. Non-Blocking Cyber HUD Overlay Lifecycle (`web/static/css/hud.css`, `web/static/js/video_hud.js`)
- Click-Through Overlay Geometry:
  - `#video-hud-overlay` is styled with `position: absolute; inset: 0; z-index: 80; pointer-events: none; opacity: 0; transition: opacity 0.3s ease;`.
  - When `.visible` is added, `opacity: 1` is applied, but `pointer-events: none` remains on the overlay root container.
  - Interactive child bars (`.video-hud-header` and `.video-hud-controls`) explicitly declare `pointer-events: auto`.
  - This ensures taps on the background pass through to `#video-stage` for tap-to-wake or tap-to-hide, and taps on `#video-pip-slot` pass through unimpeded to trigger zero-reparenting stream swaps.
- Layout Dock Isolation:
  - The bottom control bar is horizontally centered with fixed maximum width (`left: 50%; transform: translateX(-50%); max-width: 600px; bottom: 32px;`).
  - This guarantees ample physical clearance from the lower-right PiP dock (`bottom: 24px; right: 24px; width: 360px;`), eliminating hit box overlap on 1080p kiosk screens.
- Auto-Fade Inactivity Timer:
  - When revealed via `showHUD()`, an auto-fade timer is armed for 5,000ms.
  - Any tap on control buttons, slider interaction, or surface tap resets the timer via `resetAutoFade()`.
  - Upon timer expiration, `hideHUD()` removes `.visible` and clears the timer.

### B. Transport Controls & Controllable Gating (`web/static/js/video_hud.js`)
- Controllable Stream Gating:
  - Stream metadata is inspected via `activeStream.controllable`.
  - When `controllable: true`, the Play/Pause button is rendered (`style.display = ''` and `hidden` attribute removed).
  - When `controllable: false` (e.g. live CCTV/doorbell RTSP feeds), the Play/Pause button is completely hidden (`style.display = 'none'` and `hidden: true`).
- Player State Icon Synchronization:
  - Synchronizes play/pause icon with `activeStream.player_state`: displays '▶' (Play) when paused, and '⏸' (Pause) when playing.
- Action In-Flight Locks:
  - Transport actions (`POST /api/video/action` with `{"action": "toggle_playback"}`) and dismiss actions (`POST /api/video/dismiss`) are guarded by a 300ms in-flight lock to prevent touch double-tapping storms.

### C. Debounced Touch Volume Slider & Drag Isolation (`web/static/js/video_hud.js`)
- 60fps Visual Tracking with 200ms Network Throttle:
  - On `input` events, the slider value updates immediately in the DOM for fluid 60fps visual responsiveness.
  - An initial network mutation (`POST /api/audio/volume`) is dispatched immediately on touch start.
  - Subsequent inputs during continuous dragging are buffered and throttled to at most one network request every 200ms.
- Guaranteed Release Commit:
  - Releases are monitored across `pointerup`, `touchend`, `pointercancel`, `touchcancel`, and `change` events.
  - On release, any pending throttle timer is canceled, and the exact final volume is committed immediately to `POST /api/audio/volume` if it differs from the last dispatched network value.
- Active Drag Isolation & Post-Commit Cooldown Window:
  - While `this.isDragging === true`, incoming `audio.state` SSE events are strictly ignored, preventing thumb jitter.
  - Upon release commit, a 350ms cooldown window is armed (`this.commitCooldownUntil = Date.now() + 350`).
  - Any incoming `audio.state` SSE events arriving during this window (which reflect in-flight intermediate server states) are discarded, completely eliminating rubber-banding snapback.

### D. Swap-on-Tap Presentation Synchronization (`web/static/js/video.js`, `web/static/js/video_hud.js`)
- Dynamic Active Stream Resolution:
  - `getActiveStream()` evaluates `videoManager.isSwapped`.
  - When unswapped, the HUD reflects `serverPrimary`.
  - When swapped via PiP tap, the HUD automatically re-binds to `serverPip`, updating the title and dynamically displaying or hiding transport controls based on the promoted stream's controllable status.
  - `VideoPlayerManager.swapStreams()` directly invokes `hud.syncStream()`.
  - `VideoPlayerManager.exitVideoMode()` automatically invokes `hud.hideHUD()`.
- Symmetric Tap-to-Wake in Swapped State:
  - In `web/static/js/video.js`, slot click event handling is strictly symmetric: `pipSlot` only stops propagation when `!this.isSwapped` (when acting as corner dock), allowing clicks on the fullscreen video when swapped to bubble to `#video-stage`.
  - In `web/static/js/video_hud.js`, stage tap-to-wake dynamically calculates the active corner dock selector (`.video-primary-slot` when swapped, `.video-pip-slot` when unswapped). Taps on the active corner dock are ignored by the HUD so stream swapping proceeds uninterrupted, while taps anywhere on the fullscreen background reliably wake or toggle the HUD.

### E. Touch Kiosk Ergonomics (`web/static/css/hud.css`)
- Appliance-Grade Hit Areas:
  - All HUD buttons (`.video-hud-btn`) enforce `min-width: 48px; min-height: 48px;`.
  - The volume container enforces `height: 48px;`, with a custom 32px circular thumb (`.video-hud-volume-slider::-webkit-slider-thumb`) and 8px purple track.
  - `-webkit-tap-highlight-color: transparent`, `touch-action: manipulation`, and `user-select: none` prevent default browser highlight overlays and text selection boxes.

---

## 3. Alternatives Considered
- **Direct Unthrottled Network Mutations on Slider Input**: Rejected because firing HTTP POST requests on every pixel of touch drag floods the network stack with 60 requests/second, introducing request queueing and out-of-order execution on the server.
- **Pure Trailing-Edge Debounce (Wait Until Release)**: Rejected because users adjusting volume on a wall display need real-time auditory feedback while dragging rather than experiencing volume change only after releasing their finger. Throttling at 200ms provides smooth auditory feedback while capping network load to 5 requests/sec.
- **Unconditional SSE State Hydration**: Rejected because accepting incoming `audio.state` SSE events during or immediately following active dragging results in jarring visual rubber-banding due to network latency. The combination of active drag isolation and a 350ms post-commit cooldown window guarantees stable touch tracking.
- **Blocking Overlay (`pointer-events: auto` on Full Viewport)**: Rejected because placing a full-screen transparent click-blocking overlay over the stage breaks tap-to-swap gestures targeting the corner PiP dock. Using `pointer-events: none` on the overlay wrapper and `pointer-events: auto` on interactive control bars cleanly resolves gesture routing.

---

## 4. Consequences
- **Positive**:
  - Appliance-grade touch overlay with fluid 5s auto-fade lifecycle and instant tap-to-wake.
  - Seamless play/pause transport control with controllable gating and stream dismissal.
  - High-performance 60fps volume slider with 200ms network throttling, guaranteed release commit, and zero rubber-banding.
  - Zero collision with corner PiP dock and full synchronization during interactive stream swaps.
  - Zero markdown tables in documentation, manifests, or client code.
- **Negative / Risks**:
  - Extremely slow or degraded WiFi networks (>350ms latency) could potentially exceed the commit cooldown window; 350ms is tuned for local-first sub-10ms LAN operation and can be adjusted via constructor options if necessary.
