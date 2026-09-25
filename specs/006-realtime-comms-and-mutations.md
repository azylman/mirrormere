# SPEC-006: Realtime Communication & Mutation Protocol (SSE + REST)

## Status
Approved / Core Architectural Contract

## Context & Motivation
Mirrormere requires a communication protocol between the headless Go backend daemon and the various client display tiers (the 60Hz Chromium Touch Kiosk PWA and the low-power Raspberry Pi E-Ink display nodes).

While traditional dashboard projects frequently default to bidirectional WebSockets, Mirrormere intentionally adopts a **Command-Query Responsibility Segregation (CQRS)** pattern:
- **Server → Client (Query / State Push)**: Unidirectional **Server-Sent Events (SSE)**.
- **Client → Server (Command / Touch Actions)**: Standard **REST Endpoints (`POST`)**.

---

## Architectural Rationale: Why SSE + REST

```mermaid
flowchart LR
    subgraph Clients [Display Clients]
        Touch[Touch Kiosk PWA\nChromium 60Hz]
        EInk[E-Ink Renderer Node\nRaspberry Pi]
    end

    subgraph External [External Producers]
        Sidecar[Sidecars & Automations\nHome Assistant / Webhooks]
    end

    subgraph GoDaemon [Mirrormere Go Daemon]
        SSEHub[SSE Event Hub / Broadcast Channel]
        ActionHandler[REST Action Dispatcher]
        PushHandler[Push Webhook Ingestion]
        StateStore[Widget State Engine & Cache]
    end

    Touch -->|POST /api/widgets/:id/action\nDiscrete Touch Actions| ActionHandler
    Sidecar -->|POST /api/widgets/:id/push\nImmediate State Push| PushHandler
    ActionHandler -->|Mutate & Trigger Event| StateStore
    PushHandler -->|Update Cache & Broadcast| StateStore
    StateStore -->|Publish Widget State| SSEHub

    SSEHub -->|GET /api/events\ntext/event-stream| Touch
    SSEHub -->|GET /api/events\ntext/event-stream| EInk
```

### 1. Natural Protocol Separation
- Bidirectional WebSockets require custom application-level framing protocols (e.g. `{ "type": "action", "payload": ... }` vs `{ "type": "event", ... }`), re-implementing error handling, status codes, and routing over raw frames.
- SSE + REST splits concerns cleanly:
  - **Commands (Mutations)** use standard HTTP semantics (`200 OK`, `202 Accepted`, `400 Bad Request`, `502 Bad Gateway`).
  - **Queries (State Streams)** use native HTTP streaming over standard port 80/443.

### 2. Zero External Go Dependencies
- SSE requires zero third-party packages in Go. It is implemented purely with the standard library `net/http` package via the standard `http.Flusher` interface.
- WebSockets require heavy external dependencies (`nhooyr.io/websocket` or `gorilla/websocket`), socket connection pools, and custom ping/pong keep-alives.

### 3. Native Browser Resilience
- The browser's native `EventSource` API handles connection lifecycle automatically:
  - Auto-reconnect with exponential backoff on network dropouts.
  - Transparent state resumption via the `Last-Event-ID` header.
  - Zero client-side JavaScript libraries required.

### 4. Lightweight E-Ink and Headless Consumption
- A Python script or minimal Go binary on an ambient Raspberry Pi (e.g. Pi 3 B+ or Pi 4B) can consume SSE by reading line-by-line from a persistent HTTP request without asyncio event loops or complex WebSocket runtimes.

### 5. Proxy & Local Network Transparency
- SSE runs over standard HTTP/1.1 or HTTP/2. It passes smoothly through local reverse proxies (Nginx, Caddy, Envoy, and Home Assistant Ingress) without hitting 60-second WebSocket upgrade timeouts or proxy termination issues.

---

## Server-Sent Events (SSE) Specification

### 1. Connection Endpoint
- **URL**: `GET /api/events`
- **Headers**:
  ```http
  Accept: text/event-stream
  Cache-Control: no-cache
  Connection: keep-alive
  ```
- **Response Headers**:
  ```http
  HTTP/1.1 200 OK
  Content-Type: text/event-stream; charset=utf-8
  Cache-Control: no-cache
  Connection: keep-alive
  X-Accel-Buffering: no
  ```

### 2. Event Types & Wire Schemas

All messages follow standard SSE syntax: `event: <type>\nid: <id>\ndata: <json>\n\n`.

#### A. `widget.update`
Pushed when a data provider yields updated state (e.g. calendar fetch, weather refresh, or chore completion):
```http
event: widget.update
id: evt_1727216200_01
data: {"widget_id":"daily-chores","timestamp":"2026-09-24T22:15:00Z","state":"healthy","data":{"list":{"id":"chores","name":"Chores","source":"local"},"items":[{"id":"i1","title":"Take out compost","done":true,"position":0},{"id":"i2","title":"Feed cat","done":false,"position":1}]}}
```

#### B. `screen.rotate`
Emitted by the Go rotation engine when the active screen advances to the next 6×2 layout:
```http
event: screen.rotate
id: evt_1727216230_02
data: {"current_screen":1,"total_screens":2,"interval_seconds":30,"widgets":[{"widget_id":"photo-carousel","origin":[0,0],"dimensions":[3,2]},{"widget_id":"home-assistant","origin":[3,0],"dimensions":[3,2]}]}
```

#### C. `system.status`
Emitted on system-level changes (backend sync health, Wi-Fi connectivity, or sensor telemetry):
```http
event: system.status
id: evt_1727216260_03
data: {"online":true,"time":"2026-09-24T22:15:20Z","home_assistant":"connected","weather_api":"ok"}
```

#### D. `video.state`
Emitted whenever the video priority stack mutates or player transport changes (stream trigger, doorbell interruption, play/pause, or dismissal per SPEC-004):
```http
event: video.state
id: evt_1727216290_04
data: {"mode":"video","primary":{"id":"chromecast","stream_url":"http://127.0.0.1:1984/cast","type":"webrtc","player_state":"playing","controllable":true},"pip":{"id":"doorbell","stream_url":"http://homeassistant:1984/doorbell","type":"webrtc","muted":true,"timeout_seconds":45}}
```

#### E. `audio.state`
Emitted whenever host audio volume or mute state mutates:
```http
event: audio.state
id: evt_1727216295_05
data: {"volume":75,"muted":false}
```

#### F. `voice.state`
Emitted whenever the voice interaction pipeline state changes (wake word detection, listening, transcription, agent thinking, or TTS reply):
```http
event: voice.state
id: evt_1727216300_06
data: {"state":"idle","transcript":null,"reply":null,"tts_engine":null}
```

#### G. Keep-Alive Heartbeat
The server writes an empty comment line `: ping` or event every 15–30 seconds to prevent reverse proxy idle timeouts:
```http
: ping 1727216320
```

---

### 3. Connection Handshake & Initial State Hydration

When a client establishes an SSE connection to `GET /api/events`:
- **Resumption with `Last-Event-ID`**: If the request includes a valid `Last-Event-ID` header and the missed events are present in the server's in-memory ring buffer (default retention: 5 minutes / 1,000 events), the server replays only the missed events in sequence.
- **Initial Connection / Cache Expiry Hydration**: If `Last-Event-ID` is omitted, blank, or expired, the server **immediately flushes an initial state hydration batch** before streaming live events. This guarantees that cold-booting kiosks, reloaded browser PWAs, and newly booted ambient e-ink nodes never render a blank canvas while waiting for subsequent background poll cycles:

1. **Current Screen Configuration (`screen.rotate`)**:
   Immediately emits the currently active screen index, total screen count, rotation interval, and widget layout positions.
   ```http
   event: screen.rotate
   id: evt_init_01
   data: {"current_screen":0,"total_screens":2,"interval_seconds":30,"widgets":[{"widget_id":"daily-chores","origin":[0,0],"dimensions":[3,2]},{"widget_id":"calendar-agenda","origin":[3,0],"dimensions":[3,2]}]}
   ```

2. **Active Widget State Hydration (`widget.update`)**:
   Immediately emits a `widget.update` event for every widget on the active screen (sourced from each provider's last known good cache snapshot in memory or SQLite).
   ```http
   event: widget.update
   id: evt_init_02
   data: {"widget_id":"daily-chores","timestamp":"2026-09-24T22:15:00Z","state":"healthy","data":{"list":{"id":"chores","name":"Chores","source":"local"},"items":[{"id":"i1","title":"Take out compost","done":true,"position":0}]}}
   ```

3. **Active Video Pipeline State (`video.state`)**:
   Flushes the current video presentation state (idle widget display vs active primary cast/camera and optional PiP window).
   ```http
   event: video.state
   id: evt_init_03
   data: {"mode":"widgets","primary":null,"pip":null}
   ```

4. **Active Audio State (`audio.state`)**:
   Flushes the current master volume level and mute state.
   ```http
   event: audio.state
   id: evt_init_04
   data: {"volume":75,"muted":false}
   ```

5. **Active Voice Pipeline State (`voice.state`)**:
   Flushes the current voice state to hydrate microphone indicators and status badges immediately.
   ```http
   event: voice.state
   id: evt_init_05
   data: {"state":"idle","transcript":null,"reply":null,"tts_engine":null}
   ```

6. **System Health Status (`system.status`)**:
   Flushes connectivity and upstream integration health.
   ```http
   event: system.status
   id: evt_init_05
   data: {"online":true,"time":"2026-09-24T22:15:20Z","home_assistant":"connected","weather_api":"ok"}
   ```

Following the initial state hydration burst, the stream transitions seamlessly to live real-time event broadcasting and periodic keep-alive pings.

---

## Inbound Mutations, Video Actions & Push Webhooks

### 1. Widget Mutation Endpoint
- **URL**: `POST /api/widgets/{widget_id}/action`
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Payload Schema**:
  ```json
  {
    "action": "toggle_item",
    "params": {
      "item_id": "i1",
      "done": true
    }
  }
  ```
  *(Supported actions and parameter schemas conform to the target widget's specification, e.g. SPEC-008 §3 for tasks and lists).*

### 2. Widget Realtime Push Webhook Endpoint
External sidecars, local microservices, and Home Assistant automations can immediately push fresh data into Mirrormere without waiting for the widget's next background polling interval:
- **URL**: `POST /api/widgets/{widget_id}/push`
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Authentication**: None (Mirrormere runs strictly on local networks with zero authn/authz; all inbound LAN calls are fully trusted per the local-network trust model).
- **Request Payload**:
  Conforms to the standard JSON payload envelope defined in SPEC-003:
  ```json
  {
    "widget_id": "daily-chores",
    "timestamp": "2026-09-24T22:20:00Z",
    "state": "healthy",
    "data": {
      "list": {
        "id": "chores",
        "name": "Chores",
        "source": "local"
      },
      "items": [
        { "id": "i1", "title": "Take out compost", "done": true, "position": 0 },
        { "id": "i2", "title": "Feed cat", "done": false, "position": 1 }
      ]
    }
  }
  ```
  - `widget_id` (string, optional in body): If provided in the JSON body, it MUST match the `{widget_id}` in the URL path. If they differ, the server rejects the request with `400 Bad Request`.
  - `timestamp` (string, RFC 3339, optional): Timestamp of data snapshot; defaults to the server's current UTC arrival time if omitted.
  - `state` (string, optional, default `"healthy"`): `"healthy"` | `"degraded"` | `"error"`.
  - `data` (object, required): Widget-specific domain payload conforming to the widget type's schema. Missing or non-object `data` yields `400 Bad Request`.

- **Cache Ingestion & Screen-Agnostic Propagation**:
  - **Immediate Cache Ingestion**: The daemon validates the payload and updates the widget provider's in-memory state and persistent SQLite cache immediately, **regardless of whether the widget is currently visible on the active screen**.
  - **Active Screen Broadcast**: If the target widget is located on the currently active rotation screen, the server broadcasts an immediate `widget.update` SSE event across all open `/api/events` connections, rendering the update in real time without waiting for the next polling cycle.
  - **Inactive Screen Hydration**: If the target widget is on an inactive screen, the state is safely cached. When the screen rotation engine subsequently advances to that screen, the updated payload is served instantly during initial screen hydration, preventing stale flickers or lag.

- **Responses**:
  - **`200 OK`**: Push payload accepted and cache updated (and SSE broadcast emitted if widget is on active screen).
    ```json
    {
      "status": "ok",
      "widget_id": "daily-chores",
      "updated_at": "2026-09-24T22:20:00Z"
    }
    ```
  - **`400 Bad Request`**: Invalid JSON syntax, missing `data` object, or `widget_id` mismatch between path and body.
    ```json
    {
      "status": "error",
      "error": "payload widget_id 'groceries' does not match path widget_id 'daily-chores'"
    }
    ```
  - **`404 Not Found`**: Widget ID `{widget_id}` does not exist in the active server configuration.
    ```json
    {
      "status": "error",
      "error": "widget 'custom-sensor' not found in active configuration"
    }
    ```

### 3. Video Stream Lifecycle & Action Endpoints
- **Trigger Stream**: `POST /api/video/trigger`
  - **Payload**:
    ```json
    {
      "id": "chromecast",
      "stream_url": "http://127.0.0.1:1984/cast",
      "type": "webrtc",
      "priority": "persistent",
      "timeout_seconds": 0,
      "controllable": true
    }
    ```
- **Dismiss Stream**: `POST /api/video/dismiss`
  - **Payload**:
    ```json
    {
      "id": "chromecast"
    }
    ```
- **Video Action & Transport Control**: `POST /api/video/action`
  - **Payload**:
    ```json
    {
      "id": "chromecast",
      "action": "toggle_playback",
      "value": null
    }
    ```
  - Controls playback state, volume, or mute on active controllable streams (SPEC-004).

### 4. Master Audio Volume & Mute Endpoints
Controls host-level audio sink attenuation and mute states (driving WirePlumber/PipeWire over the HDMI/USB-C monitor speakers per SPEC-002 and SPEC-010) while in `widgets` mode or video presentation:

#### A. Set Master Volume (`POST /api/audio/volume`)
Sets the host master audio sink volume level:
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Request Payload**:
  ```json
  {
    "volume": 75
  }
  ```
  - `volume` (integer, required): Master volume percentage between `0` and `100` inclusive.
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "volume": 75,
    "muted": false
  }
  ```
- **Error Response (`400 Bad Request`)**:
  ```json
  {
    "status": "error",
    "error": "invalid volume: must be an integer between 0 and 100"
  }
  ```
- **Side Effects**: Attenuates host WirePlumber audio sink via `wpctl set-volume @DEFAULT_AUDIO_SINK@ <level>` (capped at the configured 80% hardware safety ceiling per SPEC-010) and broadcasts an `audio.state` SSE event.

#### B. Set / Toggle Master Mute (`POST /api/audio/mute`)
Sets or toggles the host master audio mute state:
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Request Payload** (optional or empty `{}`):
  ```json
  {
    "muted": true
  }
  ```
  - `muted` (boolean, optional): Explicit target mute state (`true` to mute, `false` to unmute). If omitted or empty `{}`: toggles current mute state.
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "muted": true,
    "volume": 75
  }
  ```
- **Error Response (`400 Bad Request`)**:
  ```json
  {
    "status": "error",
    "error": "invalid payload: muted must be a boolean"
  }
  ```
- **Side Effects**: Sets or toggles host audio mute via `wpctl set-mute @DEFAULT_AUDIO_SINK@ <state>` and broadcasts an `audio.state` SSE event.

#### C. Get Audio State (`GET /api/audio`)
Returns the current master volume and mute state:
- **Headers**:
  ```http
  Accept: application/json
  ```
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "volume": 75,
    "muted": false
  }
  ```

### 5. Screen Navigation & Rotation Endpoints

#### A. Select Screen (`POST /api/screen/select`)
Selects a specific screen directly by zero-based index:
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Request Payload**:
  ```json
  {
    "screen_index": 1
  }
  ```
  - `screen_index` (integer, required): 0-indexed screen number (`0 <= screen_index < total_screens`).
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "current_screen": 1,
    "total_screens": 2
  }
  ```
- **Error Response (`400 Bad Request`)**:
  ```json
  {
    "status": "error",
    "error": "invalid screen_index: must be between 0 and total_screens - 1"
  }
  ```
- **Side Effects**: Switches the active screen layout immediately, resets the automatic rotation interval countdown, and broadcasts a `screen.rotate` SSE event.

#### B. Advance Screen (`POST /api/screen/advance`)
Advances the active screen sequentially (designed for single-button hardware triggers or sequential gesture navigations):
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Request Payload** (optional or empty `{}`):
  ```json
  {
    "direction": "next"
  }
  ```
  - `direction` (string, optional, default `"next"`): Either `"next"` to advance forward `(current_screen + 1) % total_screens`, or `"prev"` to advance backward `(current_screen - 1 + total_screens) % total_screens`.
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "current_screen": 1,
    "total_screens": 2
  }
  ```
- **Side Effects**: Advances the active screen in the requested direction, resets the automatic rotation countdown, and broadcasts a `screen.rotate` SSE event.

#### C. Pause / Resume Rotation (`POST /api/screen/pause`)
Controls the automatic rotation timer loop:
- **Headers**:
  ```http
  Content-Type: application/json
  Accept: application/json
  ```
- **Request Payload**:
  ```json
  {
    "paused": true
  }
  ```
  - `paused` (boolean, required): `true` to pause automatic rotation; `false` to resume automatic rotation.
- **Response (`200 OK`)**:
  ```json
  {
    "status": "ok",
    "paused": true,
    "current_screen": 0
  }
  ```
- **Error Response (`400 Bad Request`)**:
  ```json
  {
    "status": "error",
    "error": "invalid payload: paused must be a boolean"
  }
  ```
- **Side Effects**:
  - `paused: true`: Suspends the automatic rotation timer loop; the display remains indefinitely on the active screen until explicitly advanced or unpaused.
  - `paused: false`: Re-arms the rotation timer using `interval_seconds` and resumes periodic rotation.

### 6. Standard Responses
- **`200 OK`**: Action executed immediately, push webhook accepted and cached, or audio settings updated:
  ```json
  { "status": "ok", "widget_id": "daily-chores", "result": { "item_id": "i1", "done": true } }
  ```
  ```json
  { "status": "ok", "widget_id": "daily-chores", "updated_at": "2026-09-24T22:20:00Z" }
  ```
  ```json
  { "status": "ok", "volume": 75, "muted": false }
  ```
- **`202 Accepted`**: Action dispatched asynchronously to an external system (e.g. Home Assistant service call or CastV2 socket).
- **`400 Bad Request`**: Unknown action, invalid parameter schema, malformed payload envelope, or path/body ID mismatch.
- **`404 Not Found`**: Widget ID or stream ID not loaded in active configuration.
- **`502 Bad Gateway`**: Upstream provider (e.g. Google API, Home Assistant, Cast socket) failed.

### 7. Optimistic UI Updates on Touch Kiosks
1. User taps a chore checkbox or HUD play/pause button on the 1080p capacitive touch display.
2. The Touch PWA immediately flips the visual state locally (< 16ms, 60fps responsiveness).
3. The PWA dispatches the respective REST action endpoint (`POST /api/widgets/{widget_id}/action` or `POST /api/video/action`).
4. If the server returns success, the subsequent SSE message (`widget.update` or `video.state`) reconciles state seamlessly.
5. If the request fails (e.g. network partition), the PWA rolls back the optimistic visual state with an error toast.

---

## Display Profile Consumption Details

### Touch Kiosk (Profile A - 60Hz Interactive)
- PWA maintains a long-lived `EventSource` connection to `/api/events`.
- In-memory reactive state tree updates instantly on `widget.update`.
- Smooth slide/flip animation triggers on `screen.rotate`.
- Touch swipe gestures reset/pause the rotation timer locally, sending a `POST /api/screen/pause` or `POST /api/screen/select` if desired.

### Ambient E-Ink (Profile B - Low-Power / E-Paper)
- In the decoupled setup (e.g. Pi 3 B+ display node + amos-pi rendering hub):
  - The rendering node connects to `/api/events`.
  - Re-rendering of the static 800×480 monochrome image occurs only on `screen.rotate` or when relevant widget state changes.
  - The display Pi downloads the pre-rendered image on trigger, completely insulating the e-paper panel from useless high-frequency refreshes.
