# Architecture Decision Record: Master Audio Coordinator and 80% Software Volume Ceiling

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #239 on `azylman/mirrormere` (SPEC-013 Phase 4, Chunk 4.1), fulfilling SPEC-004 §3, SPEC-006 §5, and SPEC-010 §4.

---

## 1. Problem Statement
Prior to Chunk 4.1:
1. **Centralized Audio Coordination Absent**: Mirrormere lacked an authoritative master volume and mute state manager across server, client, and sidecar boundaries per SPEC-006 §5.
2. **Host OS Privilege & Split-Brain Risks**: Attempting to manipulate host-level audio daemons (ALSA, PipeWire, or `wpctl`) directly requires root or host socket mounts, violates container hermeticity, introduces double-attenuation bugs, and fails on headless or remote client displays.
3. **Hardware Speaker Protection Gap**: Physical kiosk monitors (such as the UPERFECT 15.6" capacitive touchscreen with dual integrated chassis speakers) risk acoustic clipping, distortion, and blown driver elements if WebRTC audio is played at unattenuated 100% hardware output.
4. **OpenAPI Contracts & State Hydration Missing**: No REST endpoints (`GET /api/audio`, `POST /api/audio/volume`, `POST /api/audio/mute`) or event schemas existed to query or manipulate master audio, and reconnecting clients lacked audio state hydration during initial connection handshakes (`evt_init_05`).
5. **Client-Side Media Sync Missing**: The web display lacked a centralized audio manager listening to `audio.state` SSE events to dynamically regulate HTML5 media elements.

---

## 2. Decision & Architecture

### A. Authoritative Audio State Coordinator (`internal/audio/coordinator.go`)
- Thread-Safe In-Memory State: `AudioCoordinator` tracks `volume` (int, default: 75) and `muted` (bool, default: false) guarded by `sync.RWMutex`.
- Input Validation: Rejects volume percentages outside [0, 100] with `ErrInvalidVolume` ("invalid volume: must be an integer between 0 and 100").
- Lock Inversion Prevention: Mutates state under write lock, captures updated values, releases lock, and then publishes to the SSE hub and hydration sink to prevent deadlock during event delivery.
- Hydration Synchronization: Defines the `AudioStateSink` interface (`SetAudioState(*events.AudioStateData)`), synchronizing state directly with `InMemoryStateProvider` upon cold boot and mutations to guarantee that initial SSE connection handshakes (`evt_init_05`) never deliver stale audio state.
- Hub Integration: Broadcasts `audio.state` SSE events with payload `{"volume": vol, "muted": muted}` across all connected clients on every mutation.

### B. OpenAPI 3.1 & REST Server Architecture (`api/openapi.yaml`, `internal/server/audio.go`)
- Schema Contract (`api/schemas/audio.state.json`): Draft 2020-12 schema declaring required `["volume", "muted"]`, integer volume bounded to [0, 100], boolean muted, and `additionalProperties: false`.
- OpenAPI Endpoints:
  - `GET /api/audio`: Returns 200 OK with `AudioStateResponse` (`{"status": "ok", "volume": 75, "muted": false}`).
  - `POST /api/audio/volume`: Accepts `AudioVolumeRequest` (`{"volume": 80}`), validates integer bounds, and returns 200 OK `AudioStateResponse`. Missing volume, negative volume, floats, or values > 100 return 400 Bad Request with SPEC-006 §5.A error formatting.
  - `POST /api/audio/mute`: Accepts optional `AudioMuteRequest` (`{"muted": boolean}`). Empty `{}` or omitted payload toggles the mute state per SPEC-006 §5.B. Strict JSON validation ensures that non-boolean or null values are rejected with 400 Bad Request ("invalid payload: muted must be a boolean").
- Server Integration:
  - Added `AudioHandler` interface to `internal/server/server.go`: `GetAudio`, `PostAudioVolume`, and `PostAudioMute`.
  - Implemented `DefaultAudioHandler` with CORS headers (`Access-Control-Allow-Origin: *`, `OPTIONS` preflight handling), method validation (GET/HEAD vs POST), and error serialization matching `ErrorResponse`.
  - Mounted `/api/audio`, `/api/audio/volume`, and `/api/audio/mute` on `Server.mux` with `RegisterAudioHandler` support.

### C. Web Client AudioManager & 80% Software Volume Ceiling (`web/static/js/audio.js`)
- Pure Software Volume Ceiling Formula:
  `effective = (volume / 100) * 0.80 * (isDucked ? 0.20 : 1.0) * (isMuted ? 0.0 : 1.0)`
  Enforces an 80% maximum ceiling directly on media elements, protecting chassis speakers while leaving host OS PipeWire volume completely unmanipulated.
- Float Precision Hygiene: Clamps results strictly between `0.0` and `0.80`, rounding to 4 decimal places to eliminate IEEE 754 precision noise and prevent `IndexSizeError` in HTML5 media elements.
- Ducking Architecture: Plumbs `isDucked` parameter into `computeEffectiveVolume` and unit-tests ducking behavior, while holding `this.ducked = false` in `AudioManager` until Phase 6 voice assistant bringup (SPEC-011). PiP video feeds are muted (`muted = true`), not ducked.
- Immediate Media Hydration: `registerMediaElement(element)` immediately computes and sets `.volume` to avoid playback audio blasting at default browser volume 1.0 before the first SSE event.
- Dual SSE Compatibility: Supports both MirrormereSSE `.on('audio.state', cb)` and standard `EventSource.addEventListener('audio.state', cb)`.
- Disconnection Hygiene: Prunes disconnected media elements (`element.isConnected === false`) during updates to avoid memory leaks.
- Template Integration: Embedded `<script src="/static/js/audio.js"></script>` in `web/templates/display.html` and initialized `window.audioManager` in `display.js`.

---

## 3. Verification & Compliance
- **Hermetic Unit Testing**:
  - `internal/audio/coordinator_test.go`: Verified defaults, valid volume steps, out-of-bounds rejection, explicit and toggled mute states, concurrent read/write access, and schema conformance against `api/schemas/audio.state.json`. Statement coverage: 100.0%.
  - `internal/server/audio_test.go`: Verified 200, 204, 400, 405, and 500 status codes for GET/HEAD/OPTIONS/POST methods, empty payload toggle, float volume rejection, and server routing. Statement coverage: 98.1%.
  - `internal/api/api_test.go`: Regenerated Go contracts via `oapi-codegen` and verified zero drift and middleware execution. Statement coverage: 98.4%.
  - `web/test/audio.test.js`: Verified formula output across bounds (0, 25, 50, 75, 100), mute overrides, ducking scaling, string coercion, DOM lifecycle, and SSE binding using `node --test`.
- **Pre-Flight Verification**: Ran `./scripts/verify.sh --full`, confirming zero UTF-8 BOMs, clean `go vet`, strict `golangci-lint`, zero dead code, and full coverage compliance >= 95.0% across all packages.
