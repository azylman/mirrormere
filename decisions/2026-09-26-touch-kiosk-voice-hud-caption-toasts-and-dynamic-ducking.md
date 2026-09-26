# Architecture Decision Record: Touch Kiosk Voice HUD, Caption Toasts & Dynamic Video Ducking

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #249 on `azylman/mirrormere` (SPEC-013 Phase 6, Chunk 6.2), fulfilling SPEC-010 §4–§5 and SPEC-011 §2–§5.

---

## 1. Problem Statement
Prior to Chunk 6.2:
1. **Missing Visual Feedback for Active Audio Capture (Privacy & Invariant Violation)**: While Chunk 6.1 established the server-side voice state relay API and SSE event broadcasting, the Touch Kiosk web client lacked on-screen voice indicators. An appliance listening for voice commands without prominent, real-time visual feedback directly violates the Listening State Visibility Invariant (SPEC-011 §5).
2. **Transcript & Agent Reply Feedback Void**: Users speaking to the assistant on Alex's touch kiosk had no immediate visual indication of what STT recognized or what the agent replied before or during TTS playback.
3. **Audio Collision & Masking During Media Playback**: When users issue voice queries while active WebRTC video streams (e.g. Chromecast or live feeds) are playing, full-volume media audio masks both the user's speech (degrading STT accuracy) and the assistant's spoken response.
4. **Dock Spatial Collision on Touch Screen**: Centering caption toasts at the absolute bottom of the screen (`bottom: 24px` or `bottom: 32px`) would collide directly with the Touch Video HUD control bar (`bottom: 32px; height: 48px;`), blocking play/pause and volume controls.
5. **Cumulative Layout Shift (CLS) in Header**: Abruptly inserting a voice indicator into the header grid via `display: none` <-> `display: flex` causes the header clock, date, and weather pill to jitter during voice interactions.
6. **Repeat SSE Frame Toast Reopening**: If a user taps a caption toast to dismiss it while the assistant is speaking, subsequent audio chunk updates or repeat SSE frames for the same utterance could re-pop the dismissed toast card.

---

## 2. Decision & Architecture

### A. Listening State Visibility Invariant & GPU-Composited Radar Pulse (`web/static/js/voice.js`, `web/static/css/hud.css`)
- Strict Active-Listening Scoping:
  - The pulsing radar ring (`.voice-pulse-ring`) activates **strictly and exclusively** during `state === 'listening'`.
  - Once the state advances to `transcribing` or `thinking`, microphone capture has halted per SPEC-011, and the pulse ring stops immediately, transitioning the indicator capsule to a steady processing state (`.transcribing` / `.thinking`).
- 60fps GPU Compositor Animation:
  - `@keyframes voiceRadarPulse` animates strictly via `transform: scale(...)` and `opacity` on the GPU compositor (Intel Mesa/Iris via Wayland Cage).
  - Continuous box-shadow spread and element dimension resizing in keyframes are prohibited to prevent idle CPU/GPU thermal load.
- CLS-Free Header Integration:
  - `#header-voice` is styled as a centered flex container (`flex: 1; min-width: 0; display: flex; justify-content: center; align-items: center; margin: 0 16px;`) with `opacity` and `visibility` transitions rather than toggling `display: none`.
  - `#voice-transcript` enforces `min-width: 0; max-width: 480px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;` to guarantee layout stability across arbitrary utterance lengths.

### B. Floating Caption Toast Dock & Visible Engine Fallback (`web/static/css/hud.css`, `web/static/js/voice.js`)
- Spatial Clearance Above Video HUD:
  - `#voice-toast-dock` is anchored at `bottom: 96px; left: 50%; transform: translateX(-50%); z-index: 1000; pointer-events: none;`, floating gracefully above the video transport controls (`bottom: 32px; height: 48px;`) while maintaining ample clearance from the corner PiP dock.
- Cyberpunk Aesthetics & Touch Hygiene:
  - The toast card (`.voice-toast`) features a dark violet/slate glass backdrop (`background: rgba(26, 16, 48, 0.94); backdrop-filter: blur(16px);`), glowing purple cyber border, 48px minimum touch target height, and hardware-accelerated slide-up fade (`transform: translateY(16px)` -> `0`).
  - `-webkit-tap-highlight-color: transparent`, `touch-action: manipulation`, and `user-select: none` prevent default browser selection artifacts.
- Engine Indicator Badges (SPEC-011 §3.3):
  - `kokoro`: Cyan accent badge (`--mm-accent-cyan: #06b6d4`)
  - `elevenlabs`: Purple accent badge (`--mm-accent-purple: #a855f7`)
  - `piper`: Amber warning badge (`--mm-status-degraded: #f59e0b`) to visually surface when primary cloud/GPU TTS failed and local Piper offline fallback engaged.
  - If `tts_engine` is null or absent, the badge container is cleanly hidden.

### C. Dynamic Video Audio Ducking & Idempotent Attenuation (`web/static/js/audio.js`, `web/static/js/voice.js`)
- Pure Software Attenuation Formula (SPEC-010 §4):
  - `effectiveVolume = (masterVolume / 100) * 0.80 * (isDucked ? 0.20 : 1.0) * (isMuted ? 0.0 : 1.0)`
  - Active duck states: `['listening', 'transcribing', 'thinking', 'synthesizing', 'speaking']` set `isDucked = true` (attenuating active video playback to 20%).
  - Idle states: `['idle', 'error']` restore `isDucked = false` (returning volume to 100%).
- Strict Idempotency in `AudioManager`:
  - `setDucked(isDucked)` compares against `this.ducked` before executing DOM writes, preventing redundant volume assignments during 5-second `thinking` keep-alive pulses or streaming audio chunk updates.
- Late-Mount & Stream Swap Protection:
  - `registerMediaElement` immediately applies `this.getEffectiveVolume()` to newly registered or swapped video streams, preventing unattenuated audio blips if a stream is triggered or swapped while voice interaction is active.

### D. Tap-to-Dismiss Turn Lock & Safety Watchdog (`web/static/js/voice.js`)
- Tap-to-Dismiss Turn Lock:
  - When the user taps a caption toast card, `dismissToast()` sets `turnDismissed = true`, calls `e.stopPropagation()` (preventing clicks from bubbling to video play/pause or HUD wake), and hides the toast immediately.
  - While `turnDismissed === true`, subsequent audio chunks or repeat `voice.state` SSE events for the same interaction turn cannot reopen the toast.
  - The flag is reset only on transition to `idle`, `error`, or fresh `listening`.
- Disconnect Safety Watchdog:
  - If a network drop or client crash occurs while ducked, a 30-second watchdog timer automatically resets HUD state and restores video volume to 100%, preventing permanently trapped 20% audio.

---

## 3. Alternatives Considered
- **CSS `display: none` Toggle on Header Voice**: Rejected because toggling layout display causes visible shifts in the header clock and weather badge every time wake word is detected. Using fixed centering with opacity and visibility transitions delivers seamless 60fps presentation.
- **Persistent Bottom Dock at `bottom: 24px`**: Rejected due to direct physical collision with `.video-hud-controls` during video playback. Elevating to `bottom: 96px` resolves all layout conflicts without requiring conditional JavaScript repositioning.
- **Server-Side Audio Mixing / Routing**: Rejected per SPEC-011 §4. Video audio ducking is handled purely client-side in the Touch Kiosk DOM by `AudioManager`, maintaining zero core server routing overhead and zero ALSA split-brain dependencies.

---

## 4. Consequences
- **Positive**:
  - Full compliance with the Listening State Visibility Invariant (SPEC-011 §5).
  - Clear user transcript and agent reply caption toasts with visible TTS fallback badges.
  - Smooth, automatic 20% video audio ducking without DOM thrashing or unattenuated audio blips.
  - Zero CLS in header and zero collision with Touch Video HUD transport controls.
  - Complete hermetic test coverage across unit tests and Node DOM runners.
- **Negative / Risks**:
  - Extremely long TTS responses (>300 characters) wrap across multiple lines within the toast; handled gracefully via `max-width: 680px` and `word-break: break-word`.
