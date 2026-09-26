# Architecture Decision Record: CastV2 Socket Monitor Sidecar and Hardware-Accelerated go2rtc Ingest Pipeline

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #254 on `azylman/mirrormere` (SPEC-013 Phase 4, Chunk 4.4), fulfilling SPEC-004 §5, §7 and SPEC-002 §2.

---

## 1. Problem Statement
Prior to Chunk 4.4:
1. **Physical Capture Coupling**: Smart displays capturing HDMI video from streaming dongles (Chromecast) often attempt direct browser `getUserMedia` capture or monolithic CGO UVC/ALSA bindings. This couples the core display daemon to physical host hardware, breaks remote development and automated CI, and requires intrusive browser permissions.
2. **Ambient Backdrop Trap**: A physical Google Chromecast continuously outputs a 1080p60 HDMI video signal even when idle (displaying the Google Ambient Backdrop slideshow). Hardware-level signal detection cannot distinguish between active media (YouTube, Spotify, Netflix, Plex) and the idle screensaver.
3. **Missing Bidirectional Transport Bridge**: When a user casts from a mobile device or voice assistant, display touch HUD overlays must immediately reflect player state (playing, paused, buffering). Conversely, when a kiosk user taps the Play/Pause HUD button, transport actions must be forwarded upstream over the local network to pause or resume media at the Chromecast hardware source without requiring proprietary cloud APIs.

---

## 2. Decision & Architecture

Mirrormere resolves this by deploying a decoupled two-container video pipeline alongside Mirrormere Core:

### Hardware Stream Producer (`go2rtc`)
- Container Service: Runs stock `alexxit/go2rtc` mounted with `deploy/go2rtc.yaml`.
- Video Ingest: Captures HDMI video directly from MacroSilicon MS2130 USB 3.0 dongle (`/dev/video0`) and ALSA digital stereo audio (`hw:CARD=MS2130,DEV=0`).
- Hardware Transcoding: Transcodes incoming video to H.264 via Intel VAAPI/QSV on the Intel N100 GPU (`/dev/dri`) and audio to Opus (`#video=h264#hardware#audio=opus`).
- Low-Latency WebRTC: Serves low-latency (~15–30ms) WebRTC streams at `http://127.0.0.1:1984/api/webrtc?src=cast`.
- Profile Isolation: Assigned to Docker Compose profile `video`, ensuring it only runs on Profile A (Touch Kiosk) and never on Profile B (Ambient E-Ink).

### CastV2 Socket Monitor & Transport Bridge (`sidecars/cast-watcher`)
- Container Service: Lightweight, pure-Go static binary container built from `sidecars/cast-watcher/Dockerfile` running as non-root user `10001:10001`.
- Pure-Go Zero-Dependency Protobuf Codec (`wire.go`): Implements 4-byte big-endian length-prefixed `CastMessage` wire encoder and decoder with a strict 64KB OOM limit (`MaxPayloadSize = 64 * 1024`) and skipping of unknown protobuf wire types.
- CastV2 Socket Supervision (`client.go`):
  - Connects to Chromecast LAN IP over TCP port 8009 with self-signed TLS (`InsecureSkipVerify: true`).
  - Performs initial handshake with `CONNECT` to `receiver-0` on `urn:x-cast:com.google.cast.tp.connection` and requests status on `urn:x-cast:com.google.cast.receiver`.
  - Runs a 5-second heartbeat ping/pong loop and 15-second watchdog timer, force-closing dead sockets and reconnecting with exponential backoff and jitter.
  - Active Cast Detection: Detects casting when `applications[0].appId != "E8C28D3C"` (Backdrop). On active cast, connects to the application's `transportId` on `urn:x-cast:com.google.cast.tp.connection`, requests media status on `urn:x-cast:com.google.cast.media`, and dispatches `POST /api/video/trigger` to Mirrormere Core.
  - Idle Cast Dismissal: When returning to Backdrop (`E8C28D3C`) or empty applications, dispatches `POST /api/video/dismiss` to Mirrormere Core and resets internal transport state.
  - Player State Synchronization: Tracks `urn:x-cast:com.google.cast.media` status. Maps `PLAYING`, `PAUSED`, and `BUFFERING` to lowercase and posts to Core `POST /api/video/state`, triggering immediate `video.state` SSE updates across all displays. `IDLE` state is filtered internally to avoid Core enum rejection.
  - Decoupled Webhook Dispatcher: Core HTTP webhooks are dispatched asynchronously with a 2-second timeout so restarting or busy Core daemons never block socket handling.
- Transport Webhook Server (`server.go`):
  - Serves HTTP on internal port 8090 with Slowloris timeouts (`ReadHeaderTimeout: 3s`, `ReadTimeout: 5s`, `WriteTimeout: 5s`, `IdleTimeout: 30s`, `MaxHeaderBytes: 1MB`).
  - `POST /action`: Receives forwarded transport actions (`toggle_playback`, `play`, `pause`) from Core, translates them to Google Cast v2 `PLAY` or `PAUSE` media commands targeting `mediaSessionId`, and returns 200 OK.
  - `GET /healthz`: Returns connection state and active application metadata with 200 OK for container healthcheck monitoring.

---

## 3. Verification & Compliance
- **Hermetic Testing**:
  - `sidecars/cast-watcher/wire_test.go`: Verified roundtrip serialization for string and binary payloads, framing length headers, 64KB OOM enforcement, truncated frames, corrupt varints, and unsupported wire types. Statement coverage: 100.0%.
  - `sidecars/cast-watcher/client_test.go`: Verified handshake, receiver status decoding, active cast detection, app-level connection, media session status synchronization, Core trigger/dismiss/state dispatching, play/pause/toggle transport commands, backdrop transitions, watchdog timeouts, empty status arrays, and exponential reconnect backoff. Statement coverage: 95.6%.
  - `sidecars/cast-watcher/server_test.go`: Verified `POST /action` success (200), bad request (400), not active (404), upstream disconnect (502), internal error (500), and `GET /healthz` responses. Statement coverage: 100.0%.
  - `sidecars/cast-watcher/main_test.go`: Verified CLI flag parsing, environment variable overrides, stdout failure handling, help execution, and error exits. Statement coverage: 95.2%.
- **Zero Invariant Violations**:
  - Zero markdown tables in ADR or manifests.
  - Zero plaintext tokens.
  - Statement test coverage floor >= 95.0% enforced across all Go packages.
