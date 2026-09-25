# SPEC-004: Unified Video Stream API, Picture-in-Picture Priority Stack, and Cast Sidecar

## Status
Approved / Day 1 Architecture (Profile A)

## Context & Motivation
Smart wall displays frequently handle multiple video feeds:
1. **Persistent Media Streaming**: Google Cast (YouTube, Spotify, Netflix, Plex) via a physical Chromecast.
2. **High-Priority Temporary Alerts**: Front door camera streams triggered by doorbell ring events.

Traditional approaches either hardcode browser-level webcam APIs (`getUserMedia`) to capture local USB dongles or create bespoke modal popups for security cameras. Both create severe architectural issues:
- `getUserMedia` couples the browser frontend to specific host hardware, requires intrusive browser permission prompts, and breaks when testing remotely or across different devices.
- Ambient backdrop trap: A physical Chromecast continuously outputs an active 1080p60 HDMI signal even when idle (displaying landscape photos). Hardware-level signal detection cannot distinguish between active media and the idle screensaver.
- Multi-feed collisions: When a doorbell rings while casting, media must not be rudely unmounted or cut off.

Mirrormere solves this cleanly by adopting a **Hardware-Agnostic Unified Video Stream Architecture**:
- All video sources feed into a single, consistent REST API (`POST /api/video/trigger`).
- Mirrormere Core manages a first-class **Role-Based Video Priority Stack** with persistent media precedence and automatic Picture-in-Picture (PiP) docking.
- Hardware video conversion is handled by a stock **`go2rtc`** container (`deploy/go2rtc.yaml`), while protocol tracking and transport controls are handled by **`sidecars/cast-watcher`**.

---

## System Architecture

```mermaid
flowchart TD
    subgraph KioskHost [Kiosk Host - Intel N100]
        CC[Google Chromecast] -->|HDMI Video & Audio| UVC[USB 3.0 HDMI Capture Dongle\n/dev/video0 + hw:CARD=MS2109]
        
        Streamer[Stock go2rtc WebRTC Server\nhttp://127.0.0.1:1984/cast]
        CastMon[sidecars/cast-watcher\nTCP :8009 - Receiver & Media]
        
        UVC --> Streamer
        CC -.->|mDNS / CastV2 State & Media Transport| CastMon

        subgraph KioskClient [Touch Kiosk PWA - Chromium]
            Player[Primary Fullscreen Player\nChromecast Audio & Video]
            PiP[Picture-in-Picture Dock\nDoorbell Video (Muted)]
            HUD[Touch HUD\nPlay/Pause / Volume / Mute / Dismiss]
            Speakers[UPERFECT Dual Speakers\nHDMI/DP Audio Sink]
            Player --> Speakers
            HUD --> Player
        end
    end

    subgraph ExternalSources [External Alert Sources]
        Doorbell[Doorbell / Security Camera\nUniFi / Scrypted / Home Assistant]
    end

    subgraph Server [Mirrormere Core Daemon]
        API[REST API\nPOST /api/video/trigger\nPOST /api/video/dismiss\nPOST /api/video/action]
        Stack[Role-Based Video Priority Stack\nPersistent Media vs Alert PiP]
        SSE[SSE Hub\nGET /api/events]
        API --> Stack
        Stack --> SSE
    end

    CastMon -->|POST /api/video/trigger\nid: chromecast, persistent| API
    Doorbell -->|POST /api/video/trigger\nid: doorbell, temporary| API
    HUD -->|POST /api/video/action\naction: toggle_playback| API
    API -->|Dispatch Media Action| CastMon
    SSE -->|event: video.state| KioskClient
    Streamer -->|WebRTC Primary Stream| Player
    Doorbell -.->|WebRTC PiP Stream| PiP
```

---

## The Unified Video Stream API

Mirrormere Core exposes generic endpoints for video lifecycle and transport management:

### 1. Trigger Video Stream (`POST /api/video/trigger`)
Dispatched when any video source becomes active.

**Headers**:
```http
Content-Type: application/json
Accept: application/json
```

**Payload Schema**:
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

- `id` (string, required): Unique identifier for the stream source (e.g. `"chromecast"`, `"doorbell_front"`).
- `stream_url` (string, required): Playable stream URL accessible to the display client (WebRTC, HLS, or MJPEG).
- `type` (string, default `"webrtc"`): Stream format (`"webrtc"`, `"hls"`, `"mjpeg"`).
- `priority` (string, default `"persistent"`):
  - `"persistent"`: Top tier. Media streaming that remains active until explicitly stopped or disconnected (e.g. Chromecast). Retains primary fullscreen and audio precedence.
  - `"temporary"`: Alert tier. Automatically dismisses after `timeout_seconds` (e.g. doorbell ring, motion camera).
- `timeout_seconds` (integer, optional): Auto-dismiss timeout for temporary streams (default 45s). Ignored for persistent streams.
- `controllable` (boolean, optional, default `false`): Set to `true` if the stream supports remote transport actions (play/pause/mute).

### 2. Dismiss Video Stream (`POST /api/video/dismiss`)
Dispatched when media stops or when manually dismissed by a client.

**Payload Schema**:
```json
{
  "id": "chromecast"
}
```

### 3. Video Action & Transport Control (`POST /api/video/action`)
Dispatched by touch interaction or companion controllers to manipulate active media streams.

**Payload Schema**:
```json
{
  "id": "chromecast",
  "action": "toggle_playback",
  "value": null
}
```

- `id` (string, required): Stream identifier to control.
- `action` (string, required): Supported actions:
  - `"toggle_playback"`: Toggles play/pause state.
  - `"play"`: Resumes playback.
  - `"pause"`: Pauses playback.
  - `"mute"`: Mutes stream audio.
  - `"unmute"`: Unmutes stream audio.
  - `"volume"`: Adjusts stream volume (`value`: float `0.0`–`1.0`).

---

## Role-Based Video Priority Stack & Display Modes

The Touch Kiosk UI supports two primary display modes:
1. **`widgets` Mode**: The standard 6×2 grid canvas with fixed header (SPEC-005).
2. **`video` Mode**: Dedicated video presentation (fullscreen primary with optional corner PiP dock).

### Precedence Hierarchy: Media Retains Primary Focus
To prevent doorbell alerts from interrupting movies, music, or cooking tutorials:
- **`persistent` (Chromecast)** has **absolute priority** over `temporary` alert streams.
- If Chromecast is playing, it **remains fullscreen with audio uninterrupted**.
- Incoming alert streams (doorbells) dock into a floating **Picture-in-Picture (PiP)** window with **no audio**.

```
[ Primary Slot (Fullscreen + Audio Focus) ]  -> Persistent Media (Chromecast), or Alert when idle
[ PiP Dock Slot (Corner Overlay, Muted)   ]  -> Temporary Alert (Doorbell) when persistent media active
```

### Transition State Machine

1. **Idle State (`widgets` mode)**:
   - When a `persistent` stream triggers (Chromecast): Display transitions to `video` mode, mounting Chromecast fullscreen with audio.
   - When a `temporary` stream triggers (Doorbell while not casting): Display transitions to `video` mode, mounting the doorbell fullscreen with audio for `timeout_seconds`, then returning to `widgets`.
2. **Doorbell Interrupts Active Cast (PiP Docking)**:
   - When a doorbell rings while Chromecast is already playing:
     - **Chromecast stays full-screen**: Audio continues playing uninterrupted at normal volume.
     - **Doorbell mounts in PiP**: A floating corner window appears in the lower-right corner displaying the live camera feed **with audio muted** (`muted = true`).
     - Tapping the PiP window allows the user to manually swap PiP and fullscreen if desired.
3. **Alert Dismissal**:
   - When the doorbell timer expires (default 45s) or user taps dismiss on the PiP card:
     - The PiP overlay unmounts cleanly.
     - The fullscreen Chromecast stream is completely unaffected.
4. **Casting Disconnects**:
   - When casting stops:
     - If an alert is still active in PiP, it expands to fullscreen.
     - If no streams remain, display returns to `widgets` mode.

### SSE Wire Schema (`video.state`)

Whenever the priority stack mutates or player transport state changes, Mirrormere broadcasts a `video.state` event across `GET /api/events` (SPEC-006):

```http
event: video.state
id: evt_1727221200_01
data: {"mode":"video","primary":{"id":"chromecast","stream_url":"http://127.0.0.1:1984/cast","type":"webrtc","player_state":"playing","controllable":true},"pip":{"id":"doorbell","stream_url":"http://homeassistant:1984/doorbell","type":"webrtc","timeout_seconds":45,"muted":true}}
```

When no videos remain active:
```http
event: video.state
id: evt_1727221245_02
data: {"mode":"widgets","primary":null,"pip":null}
```

---

## Kiosk Video Ingest & Cast Services

To keep Mirrormere Core and the Touch PWA completely hardware-agnostic, physical USB capture and Cast protocol tracking are decoupled into two distinct container services running on the kiosk host:

### 1. Hardware Stream Producer (`go2rtc`)
The physical Chromecast plugs into a USB 3.0 HDMI capture card (MacroSilicon MS2109 or similar).
A stock `alexxit/go2rtc` container runs with `deploy/go2rtc.yaml` mounted:
```yaml
# deploy/go2rtc.yaml
streams:
  cast:
    - ffmpeg:device?video=/dev/video0&audio=hw:CARD=MS2109#video=copy#audio=opus
```
- Ingests 1080p60 HDMI video directly over `/dev/video0` (UVC) and digital audio over ALSA (UAC).
- Serves sub-10ms, hardware-accelerated WebRTC locally at `http://127.0.0.1:1984/cast`.

### 2. CastV2 Socket Monitor & Transport Bridge (`sidecars/cast-watcher`)
Chromecast continuously outputs 1080p HDMI video even when idle, rendering the Google Ambient Backdrop slideshow.
The custom `sidecars/cast-watcher` container connects directly to the Chromecast's LAN IP over TCP port 8009 (**Google Cast v2 protocol**):
- Subscribes to receiver status (`urn:x-cast:com.google.cast.receiver`) and media session status (`urn:x-cast:com.google.cast.media`).
- **Active Casting Detected**: When `applications[0].appId != "E8C28D3C"` (not the Backdrop app), casting is active. The sidecar immediately dispatches `POST /api/video/trigger` with `priority: "persistent"` and `controllable: true`.
- **Casting Disconnected**: When status returns to Backdrop or empty, the sidecar dispatches `POST /api/video/dismiss`.
- **Bidirectional Transport Control**:
  - When the kiosk user taps the Play/Pause HUD button, Mirrormere dispatches the action to the sidecar.
  - The sidecar transmits a CastV2 `PAUSE` or `PLAY` message to the active media session on `:8009`.
  - The upstream media (Spotify, YouTube, Netflix, Plex) pauses/resumes cleanly at the Chromecast source.
  - The sidecar reads `mediaStatus.playerState` (`PLAYING` / `PAUSED`) and updates Mirrormere Core so the HUD icon stays 100% in sync regardless of whether playback was paused via phone, voice, or screen touch.

---

## Unified WebRTC Audio Pipeline & Controls

Because audio is packaged directly into the WebRTC stream alongside video, Chromium's standard HTML5 `<video>` element acts as the single source of truth for audio output.

### 1. Audio Routing & Precedence
- **Audio Output**: Chromium plays audio through the default ALSA/PipeWire sink, driving the UPERFECT monitor's integrated dual stereo speakers over HDMI/USB-C.
- **Audio Precedence**:
  - The primary fullscreen video holds exclusive audio focus (`primaryVideo.muted = false`).
  - Secondary streams docked into PiP (doorbell/alerts) are **strictly muted** (`pipVideo.muted = true`).
  - When a doorbell rings during a Chromecast session, **Chromecast audio continues playing completely uninterrupted**.
- **Voice Assistant Ducking (SPEC-011)**: When an assistant wake word fires or speech plays, video audio ducks smoothly to **20%**, restoring when speech completes.

### 2. Touch HUD & Transport Controls
- **Remote / Phone Volume**: When casting from a phone, the user's phone volume rocker attenuates audio digitally at the Chromecast hardware source via CastV2.
- **On-Screen Touch HUD**: Tapping anywhere on the video screen displays a floating cyber HUD overlay (auto-fading after 3s of inactivity) containing:
  - **Play / Pause Transport Toggle**: Large (minimum 48×48px) touch-friendly button in the control bar. Tapping toggles media playback via CastV2 upstream.
  - **Mute / Unmute Toggle**: Toggles audio mute state.
  - **Volume Slider**: Linear touch slider (0–100%) controlling `video.volume` (persisted in `localStorage`).
  - **Dismiss ('X') Button**: Unmounts video mode immediately and returns to `widgets` mode.
- **Non-Controllable Streams**: For live camera and doorbell feeds (`controllable: false`), the play/pause transport button is hidden.
- **Host Hardware Ceiling**: WirePlumber configures an 80% maximum volume ceiling on boot to prevent chassis speaker distortion or clipping.
