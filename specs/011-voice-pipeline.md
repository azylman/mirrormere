# SPEC-011: Unified Voice Pipeline (Hub-and-Spoke Streaming, Wake Word, LAN GPU Acceleration, and HUD Display Events)

## Status
Approved / Phase 2 Reference Architecture (SPEC-001)

## Context & Motivation
Both reference households deploy microphones and speakers on their wall hardware:
- **Alex's Touch Kiosk (Profile A)**: Intel N100 Mini PC behind a 15.6" UPERFECT touchscreen monitor with a USB microphone and integrated monitor speakers, backed by a dedicated **NVIDIA Jetson Orin** on the local network for GPU inference (Whisper STT and Kokoro TTS) and **Aerial** as the autonomous agent brain.
- **Mike's Ambient E-Ink (Profile B)**: Raspberry Pi 3 Model B+ (`amos-pi`) with a Waveshare 7.5" e-paper HAT, **reSpeaker XVF3800** 4-mic array, and living room audio, backed by an NVIDIA GPU desktop on the LAN for Whisper STT, **Amos** as the autonomous agent brain, and ElevenLabs TTS with local Piper fallback.

### The Architectural Problem: Thick Edge vs. Hub-and-Spoke
Earlier drafts required the edge unit (kiosk / Pi) to coordinate every stage: calling STT, waiting for text, calling the agent brain, waiting for reply text, calling TTS, and managing fallback engines. This placed excessive configuration burden, network latency, and secret management onto the wall hardware.

To achieve clean separation of concerns, Mirrormere adopts a **Hub-and-Spoke Streaming Architecture**:
1. **Dumb Edge Audio Terminal & HUD Presenter**: The edge dock only knows how to listen for the local wake word, stream recorded audio to a single **Voice Hub** endpoint, play returned audio on its speakers, and reflect status changes on the display HUD.
2. **Coordinated Voice Hub**: A dedicated LAN gateway (Alex's Jetson Orin sidecar; Mike's always-on `amos-pi`) orchestrates STT, agent brain routing, and TTS synthesis.
   - On Alex's network: The Jetson Orin runs Whisper and Kokoro locally on GPU and calls Aerial over HTTP.
   - On Mike's network: `amos-pi` runs the Voice Hub, reaching out to his desktop GPU PC for Whisper (avoiding WSL2 port-forwarding issues for inbound traffic), routing through the HESTIA queue to Amos, and calling ElevenLabs with local Piper fallback directly on `amos-pi`.
3. **Streaming Event Feedback & Keep-Alives**: The Hub returns Server-Sent Events (SSE) over the active interaction stream. As soon as STT finishes, the transcript is streamed back so the kiosk HUD immediately renders the user's words; periodic `thinking` pulses keep connections alive during queued agent deliberation; when the brain replies, the reply text is streamed back for caption toasts; synthesized audio chunks (PCM, WAV, or MP3) stream back for immediate playback.

---

## Unified System Architecture

```mermaid
flowchart TD
    subgraph Dock [Display Dock / Edge Unit]
        Mic[Microphone Input\nreSpeaker DSP / USB Mic + PipeWire AEC] --> Ear
        subgraph VoiceClient [mirrormere-voice Client]
            Ear[1. Ear: VAD + Local Wake Word\nopenWakeWord 'Hey Aerial' / 'Hey Amos']
            Rec[Utterance Capture\nSilence VAD Gate]
            Relay[Event & State Relay]
            Play[5. Audio Playback Controller\nMonitor / Pi Audio + Barge-In]
        end
        Ear -->|wake event| Rec
        Ear -->|state: listening| Relay
        Rec -->|POST /api/voice/interact\nWAV Audio| Hub
    end

    subgraph Hub [LAN Voice Hub Gateway\nJetson Orin (Alex) / amos-pi (Mike)]
        Router[Interaction Controller]
        STT[2. STT: faster-whisper large-v3\nAlex: Jetson Orin / Mike: Desktop GPU]
        Brain[3. Agent Brain: http_agent\nAerial (Ameridroid) / Amos (HESTIA Queue)]
        TTS[4. TTS Engine\nAlex: Kokoro-82M on Orin\nMike: ElevenLabs + Piper Fallback]
        
        Router --> STT
        STT -->|Transcript JSON| Brain
        Brain -->|Reply Text| TTS
        TTS -->|Audio Chunks| Router
    end

    subgraph MirrormereCore [Mirrormere Core & Display]
        API[Voice State Ingress\nPOST /api/voice/state]
        SSE[SSE Event Stream Bus\nGET /api/events\nevent: voice.state]
        Ducker[Video Audio Ducking\nChromecast -> 20%]
        HUD[Display Surface\nTouch Kiosk HUD / E-Ink Status Widget]
        
        API --> SSE
        API --> Ducker
        SSE --> HUD
    end

    Hub -->|SSE: state=transcribing| Relay
    Hub -->|SSE: transcript='...' (state=thinking)| Relay
    Hub -->|SSE: thinking keep-alive pulse| Relay
    Hub -->|SSE: reply='...' (state=speaking)| Relay
    Hub -->|SSE: audio_chunk (wav/pcm/mp3)| Play
    Hub -->|SSE: error (on timeout/failure)| Relay
    Hub -->|SSE: done (state=idle)| Relay

    Relay -->|POST /api/voice/state\nState + Transcript + Reply| API
```

---

## The Streaming Interaction Contract (`POST /api/voice/interact`)

The edge dock communicates with the Voice Hub via a single bidirectional HTTP interaction endpoint.

### 1. Ingress Request
- **Method**: `POST /api/voice/interact`
- **Content-Type**: `multipart/form-data`
- **Fields**:
  - `audio`: Recorded PCM WAV file (16 kHz mono 16-bit).
  - `node_id`: Edge dock identifier (`touch-kiosk-kitchen` or `eink-display-livingroom`).
  - `speaker`: Optional speaker hint if identified by dock.
  - `session_id`: Unique interaction trace ID.

### 2. Streaming Response (`text/event-stream`)
The Hub holds the HTTP response open, streaming chunked Server-Sent Events as processing progresses:

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Transfer-Encoding: chunked

event: state
data: {"state": "transcribing"}

event: transcript
data: {"state": "thinking", "text": "What time is Alex's next meeting?"}

event: thinking
data: {"state": "thinking", "elapsed_seconds": 5}

event: reply
data: {"state": "speaking", "text": "Your next meeting is Project Sync at 2:00 PM."}

event: audio_chunk
data: {"chunk_index": 0, "format": "mp3", "is_final": true, "data": "<base64-audio>"}

event: done
data: {"state": "idle", "duration_ms": 1240}
```

### 3. Event Specifications
- `event: state`: Notifies the edge of active sub-state transitions (`transcribing`, `synthesizing`).
- `event: transcript`: Fired immediately when Whisper finishes transcription. Contains the recognized user text.
- `event: thinking`: Keep-alive heartbeat emitted every 5 seconds while waiting for agent brains with asynchronous task queues (e.g. Amos via HESTIA, which may take 10–60s). Prevents reverse proxies, client HTTP timeouts, and socket drops.
- `event: reply`: Fired when the agent brain returns its answer text, enabling instant caption toast rendering.
- `event: audio_chunk`: Carries audio payload. Supports `format: "pcm"`, `format: "wav"`, or `format: "mp3"`. Allows either multi-chunk streaming (Kokoro) or a single complete payload (ElevenLabs / Piper) indicated by `is_final: true`.
- `event: error`: Emitted if the brain or STT exceeds configured timeouts (e.g. 60s) or encounters fatal exceptions:
  ```json
  { "error": "brain_timeout", "message": "Agent brain did not respond within 60s" }
  ```
  Upon receiving `event: error`, the edge client logs the failure, resets HUD state to `idle`, and does not play broken audio.
- `event: done`: Signals completion of synthesis and turn closure.

### 4. Edge Dock Relay to Mirrormere Display
Upon receiving each event over the interaction stream, the dock's `mirrormere-voice` client immediately forwards the state to local Mirrormere Core via `POST /api/voice/state`:
- **Touch Kiosk (Profile A)**:
  - `event: transcript` ➔ Sets state to `thinking` and renders recognized query in the pinned header.
  - `event: reply` ➔ Sets state to `speaking` and pops a cyberpunk caption toast along the bottom HUD while active video ducks to 20%.
  - `event: audio_chunk` ➔ Audio chunks stream into PipeWire ALSA buffer for speaker output.
  - `event: done` ➔ HUD clears toasts, returns to `idle`, and video un-ducks.
- **Ambient E-Ink (Profile B)**:
  - The voice satellite receives the hub's events directly, but the e-paper panel is drawn from the daemon's image. The satellite forwards state changes to Mirrormere daemon's `POST /api/voice/state`, updating `voice.state` on the SSE bus so the 2×1 status widget renders the recognized text or mic glyph on the next coalesced e-ink refresh.

---

## Component Responsibilities & Operational Invariants

### 1. Ear & Dock Presentation (Edge Unit: N100 / Pi 3 B+)
- **Local Wake Word**: `openWakeWord` runs locally on the CPU ("Hey Aerial" / "Hey Amos"). Zero raw audio is transmitted across the LAN until wake verification completes.
- **Echo Cancellation (AEC)**:
  - **Alex (Touch Kiosk)**: PipeWire `module-echo-cancel` (`webrtc-aec`) using monitor speakers as reference channel.
  - **Mike (Ambient E-Ink)**: reSpeaker XVF3800 hardware DSP AEC.
- **Barge-In**:
  - If a wake word is detected while the assistant is speaking, audio playback halts immediately (< 50ms) and the ear resets to active capture.

### 2. Voice Hub Orchestration
- **Alex's Hub (NVIDIA Jetson Orin)**:
  - Co-locates `faster-whisper large-v3` (CUDA) and `kokoro-82m` (CUDA/TensorRT, `POST /v1/audio/speech`).
  - Calls Aerial Brain over HTTP (`POST /api/voice/ask`).
- **Mike's Hub (`amos-pi` / Raspberry Pi 3 B+)**:
  - Runs on the always-on `amos-pi`, calling Mike's desktop PC for Whisper STT over LAN.
  - Routes turns to Amos via the HESTIA queue with periodic `thinking` pulses.
  - Synthesizes speech via ElevenLabs with local Piper fallback.

### 3. TTS Fallback Invariant (Zero Sticky State)
1. **Primary First on Every Turn**:
   - For Mike: ElevenLabs (`eleven_multilingual_v2`) is attempted on **every single reply** with a strict **10-second timeout**.
   - For Alex: Kokoro-82M on Jetson Orin is attempted on every single reply with a 5-second timeout.
2. **Deterministic Offline Fallback (Piper)**:
   - Piper (`en_US-lessac-medium`) executes only when the primary engine encounters a timeout, 429 rate limit, HTTP 5xx error, or network refusal.
3. **Audit Logging & Zero Sticky State**:
   - Every fallback event logs its exact failure cause (`timeout`, `rate_limit`, `connection_refused`) to `voice_fallback.log`.
   - The engine never sticks in fallback mode; every subsequent utterance retries the configured primary engine.

### 4. Playback Safety & Privacy Rules
1. **Quiet Hours**:
   - Suppresses audible speech during configured hours (e.g. `21:00-07:00` for Mike; `23:00-07:00` for Alex). Responses during quiet hours are routed exclusively to display widgets and caption toasts without emitting sound.
2. **Mute Switch Protection**:
   - Physical or software privacy mute status is verified prior to activating audio capture or playback.
3. **No Unconfirmed Live Tests**:
   - Automated tests and verification scripts must NEVER trigger unprompted audible sound on physical speakers without explicit, prior confirmation from the household owner (Alex / Mike). Test pipelines must mock the ALSA/PipeWire playback sink.

---

## Reference Configurations

### 1. Alex's Wall Kiosk (`/etc/mirrormere/voice.yaml` on N100 Kiosk)
```yaml
voice:
  enabled: true
  node_id: "touch-kiosk-kitchen"

  ear:
    engine: openwakeword
    wake_phrases: ["hey aerial"]
    silence_ms: 800
    followup_seconds: 8
    aec:
      enabled: true
      backend: pipewire_echo_cancel

  hub:
    url: "http://192.168.1.180:9000/api/voice/interact"   # Jetson Orin Voice Hub
    timeout_seconds: 60

  playback:
    sink: "alsa_output.pci-0000_00_1f.3.hdmi-stereo"
    quiet_hours: "23:00-07:00"
    duck_video_percent: 20

  display:
    mirrormere_url: "http://localhost:8080"
```

### 2. Alex's Voice Hub (`/etc/voice-hub/config.yaml` on Jetson Orin)
```yaml
hub:
  listen: "0.0.0.0:9000"

  stt:
    engine: faster_whisper
    model: large-v3
    device: cuda
    compute_type: float16

  brain:
    adapter: http_agent
    url: "http://192.168.1.77:4000/api/voice/ask"       # Aerial Brain on Ameridroid
    timeout_seconds: 30

  tts:
    primary: kokoro
    kokoro:
      endpoint: "http://localhost:8880/v1/audio/speech" # Local Kokoro container on Orin
      voice: "af_bella"
      timeout_seconds: 5
    fallback: piper
    piper:
      voice: en_US-lessac-medium
```

### 3. Mike's Ambient E-Ink (`/etc/mirrormere/voice.yaml` on Pi 3 B+)
```yaml
voice:
  enabled: true
  node_id: "eink-display-livingroom"

  ear:
    engine: openwakeword
    wake_phrases: ["hey amos"]
    silence_ms: 800
    followup_seconds: 8
    aec:
      enabled: true
      backend: respeaker_hardware_dsp

  hub:
    url: "http://localhost:9000/api/voice/interact"      # Voice Hub running locally on amos-pi
    timeout_seconds: 75

  playback:
    sink: "default"
    quiet_hours: "21:00-07:00"
    duck_video_percent: 20

  display:
    mirrormere_url: "http://localhost:8080"
```

### 4. Mike's Voice Hub (`/etc/voice-hub/config.yaml` on `amos-pi` / Pi 3 B+)
```yaml
hub:
  listen: "0.0.0.0:9000"

  stt:
    engine: whisper_http
    url: "http://192.168.1.185:9099/transcribe"         # Desktop CUDA PC LAN IP
    timeout_seconds: 10
    fallback: whisper_local
    whisper_local:
      model: small
      compute_type: int8

  brain:
    adapter: http_agent
    url: "http://192.168.1.185:8000/agent/ask"          # Amos Agent API via HESTIA Queue
    timeout_seconds: 60
    keepalive_interval_seconds: 5

  tts:
    primary: elevenlabs
    elevenlabs:
      model: eleven_multilingual_v2
      voice_id_env: AMOS_ELEVENLABS_VOICE_ID
      timeout_seconds: 10
    fallback: piper
    piper:
      voice: en_US-lessac-medium

  logging:
    fallback_log: "/var/log/mirrormere/voice_fallback.log"
```
