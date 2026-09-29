# SPEC-011: Unified Voice Pipeline (Hub-and-Spoke Streaming, Wake Word, LAN GPU Acceleration, and HUD Display Events)

## Status
Approved / Phase 2 Reference Architecture (SPEC-001)

## Context & Motivation
Both reference households deploy microphones and speakers on their wall hardware:
- **Alex's Touch Kiosk (Profile A)**: Intel N100 Mini PC behind a 15.6" UPERFECT touchscreen monitor with a USB microphone and integrated monitor speakers, backed by a dedicated **NVIDIA Jetson Orin** on the local network for GPU inference (Whisper STT and Kokoro TTS) and **Aerial** as the autonomous agent brain.
- **Mike's Ambient E-Ink (Profile B)**: Raspberry Pi 4 Model B with a Waveshare 7.5" V2 raw e-paper panel driven by an Adafruit E-Ink Bonnet (with custom GPIO pin mappings per SPEC-002 and SPEC-009), **reSpeaker XVF3800** 4-mic array, and living room audio, backed by an NVIDIA GPU desktop on the LAN for Whisper STT, **Amos** as the autonomous agent brain, and ElevenLabs TTS with local Piper fallback.

### The Architectural Problem: Thick Edge vs. Hub-and-Spoke
Earlier drafts required the edge unit (kiosk / Pi) to coordinate every stage: calling STT, waiting for text, calling the agent brain, waiting for reply text, calling TTS, and managing fallback engines. This placed excessive configuration burden, network latency, and secret management onto the wall hardware.

To achieve clean separation of concerns, Mirrormere adopts a **Hub-and-Spoke Streaming Architecture**:
1. **Dumb Edge Audio Terminal & HUD Presenter**: The edge dock only knows how to listen for the local wake word, stream recorded audio to a single **Voice Hub** endpoint, play returned audio on its speakers, and reflect status changes on the display HUD.
2. **Coordinated Voice Hub**: A dedicated LAN gateway (Alex's Jetson Orin sidecar; Mike's always-on voice hub) orchestrates STT, agent brain routing, and TTS synthesis.
   - On Alex's network: The Jetson Orin runs Whisper and Kokoro locally on GPU and calls Aerial over HTTP.
   - On Mike's network: The Voice Hub runs on the Pi 4B, reaching out to his desktop GPU PC for Whisper (avoiding WSL2 port-forwarding issues for inbound traffic), routing through the agent task queue to Amos, and calling ElevenLabs with local Piper fallback directly on the Pi 4B.
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

    subgraph Hub [LAN Voice Hub Gateway\nJetson Orin (Alex) / Pi 4B (Mike)]
        Router[Interaction Controller]
        STT[2. STT: faster-whisper large-v3\nAlex: Jetson Orin / Mike: Desktop GPU]
        Brain[3. Agent Brain: http_agent\nAerial / Amos]
        TTS[4. TTS Engine\nAlex: Kokoro-82M on Orin\nMike: ElevenLabs + Piper Fallback]
        
        Router --> STT
        STT -->|Transcript JSON| Brain
        Brain -->|Reply Text| TTS
        TTS -->|Audio Chunks| Router
    end

    subgraph MirrormereCore [Mirrormere Core & Display]
        API[Voice State Ingress\nPOST /api/voice/state]
        SSE[SSE Event Stream Bus\nGET /api/events\nevent: voice.state]
        HUD[Display Surface\nTouch Kiosk HUD]
        
        API --> SSE
        SSE --> HUD
    end

    Hub -->|SSE: state=transcribing| Relay
    Hub -->|SSE: transcript='...' (state=thinking)| Relay
    Hub -->|SSE: thinking keep-alive pulse| Relay
    Hub -->|SSE: reply='...' (state=speaking)| Relay
    Hub -->|SSE: audio_chunk (wav/pcm/mp3)| Play
    Hub -->|SSE: error (on timeout/failure)| Relay
    Hub -->|SSE: done (state=idle)| Relay

    Relay -->|POST /api/voice/state\nState + Transcript + Reply + TTS Engine| API
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
  - `session_id`: (Optional) Unique interaction trace ID per turn. Generated by the Voice Hub if omitted by the client. Brain implementations wanting a persistent session per physical device key on `node_id`.

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
data: {"transcript": "What time is Alex's next meeting?"}

event: thinking
data: {"elapsed_seconds": 5}

event: reply
data: {"reply": "Your next meeting is Project Sync at 2:00 PM.", "tts_engine": "kokoro"}

event: audio_chunk
data: {"chunk_index": 0, "format": "mp3", "is_final": true, "data": "<base64-audio>"}

event: done
data: {"duration_ms": 1240}
```

### 3. Event Specifications
- `event: state`: Notifies the edge of active sub-state transitions (`transcribing`, `synthesizing`).
- `event: transcript`: Fired immediately when Whisper finishes transcription. Contains the recognized user `transcript`.
- `event: thinking`: Keep-alive heartbeat emitted every 5 seconds while waiting for agent brains with asynchronous task queues (which may take 10–60s). Prevents reverse proxies, client HTTP timeouts, and socket drops by continuously resetting client read/idle timers.
- `event: reply`: Fired when the agent brain returns its answer text, enabling instant caption toast rendering. Contains `reply` text and the active `tts_engine`.
- `event: audio_chunk`: Carries audio payload. Supports `format: "pcm"`, `format: "wav"`, or `format: "mp3"`. Allows either multi-chunk streaming (Kokoro) or a single complete payload (ElevenLabs / Piper) indicated by `is_final: true`.
- `event: error`: Emitted if the brain or STT exceeds configured timeouts (e.g. 60s) or encounters fatal exceptions:
  ```json
  { "error": "brain_timeout", "message": "Agent brain did not respond within 60s" }
  ```
  Upon receiving `event: error`, the edge client logs the failure, resets HUD state to `idle`, and does not play broken audio.
- `event: done`: Signals completion of synthesis and turn closure.

### 4. Edge Dock Relay to Mirrormere Display
Upon receiving interaction lifecycle events, the dock's `mirrormere-voice` client immediately forwards the state to local Mirrormere Core via `POST /api/voice/state` (defined in SPEC-006 §6):
- **Wire Payload Schema (`POST /api/voice/state`)**:
  ```json
  {
    "state": "thinking",
    "transcript": "What time is Alex's next meeting?",
    "reply": null,
    "tts_engine": null
  }
  ```
  Core validates the payload, updates its in-memory voice state cache, and rebroadcasts it across `GET /api/events` as a `voice.state` SSE event to synchronize all connected screens.
- **Relay Lifecycle Progression**:
  - `Wake Detected`: Client immediately posts `state: "listening"` (`transcript: null`, `reply: null`, `tts_engine: null`). Touch Kiosk PWA automatically ducks active video playback to 20% in the DOM.
  - `event: state (transcribing)`: Client posts `state: "transcribing"` (`transcript: null`, `reply: null`, `tts_engine: null`).
  - `event: transcript`: Client posts `state: "thinking"` with the recognized `transcript`. Touch Kiosk renders the recognized query in the pinned header; E-Ink hardware LED ring indicates processing without e-paper refreshes.
  - `event: reply`: Client posts `state: "speaking"` with `transcript`, `reply`, and `tts_engine`. Touch Kiosk pops a caption toast along the bottom HUD while video remains ducked at 20%.
  - `event: audio_chunk`: Chunks stream directly into the local PipeWire/ALSA playback buffer.
  - `event: done` or `event: error`: Client posts `state: "idle"` (or `state: "error"` on unrecovered failure) with null values. HUD clears toasts, returns to idle, and active video volume un-ducks.
  *(Note: Video ducking is managed strictly client-side by the Touch Kiosk PWA in response to `voice.state` SSE events; Mirrormere Core does not process or route audio streams).*

---

## Component Responsibilities & Operational Invariants

### 1. Ear & Dock Presentation (Edge Unit: N100 / Pi 4B)
- **Local Wake Word**: `openWakeWord` runs locally on the CPU ("Hey Aerial" / "Hey Amos"). Zero raw audio is transmitted across the LAN until wake verification completes.
- **Listening State Visibility Invariant**: The listening state is ALWAYS visible on at least one surface whenever the microphone is actively capturing audio (e.g. live pulsing visual indicator on Touch Kiosk HUD, or hardware LED on reSpeaker XVF3800). A device that listens without showing it is a bug.
- **Echo Cancellation (AEC)**:
  - **Alex (Touch Kiosk)**: PipeWire `module-echo-cancel` (`webrtc-aec`) using monitor speakers as reference channel.
  - **Mike (Ambient E-Ink)**: reSpeaker XVF3800 hardware DSP AEC.
- **Barge-In**:
  - If a wake word is detected while the assistant is speaking, audio playback halts immediately (< 50ms) and the ear resets to active capture.

### 2. Voice Hub Orchestration
- **Alex's Hub (NVIDIA Jetson Orin)**:
  - Co-locates `faster-whisper large-v3` (CUDA) and `kokoro-82m` (CUDA/TensorRT, `POST /v1/audio/speech`).
  - Calls Aerial Brain over HTTP (`POST /api/voice/ask`).
- **Mike's Hub (Raspberry Pi 4B)**:
  - Runs on the always-on Pi 4B, calling Mike's desktop PC for Whisper STT over LAN.
  - Routes turns to Amos via the agent task queue with periodic `thinking` pulses.
  - Text Channel Record: The `http_agent` adapter also posts the transcribed prompt and reply text to a dedicated Discord text channel as a persistent interaction record.
  - Synthesizes speech via ElevenLabs with local Piper fallback.

### 3. TTS Fallback Invariant (Zero Sticky State)
1. **Primary First on Every Turn**:
   - For Mike: ElevenLabs (`eleven_multilingual_v2`) is attempted on **every single reply** with a strict **10-second timeout**.
   - For Alex: Kokoro-82M on Jetson Orin is attempted on every single reply with a 5-second timeout.
2. **Deterministic Offline Fallback (Piper)**:
   - Piper (`en_US-lessac-medium`) executes only when the primary engine encounters a timeout, 429 rate limit, HTTP 5xx error, or network refusal.
3. **Audit Logging & Visible Fallback**:
   - Every fallback event logs its exact failure cause (`rate_limited`, `timeout`, `network_error`, `http_error:NNN`, `empty_response`) to `voice_fallback.log`, so a voice-quality downgrade is visible afterwards, not silent.
   - Fallback status is propagated in `POST /api/voice/state` and `voice.state` via `tts_engine: "piper"` (or `tts_engine: "elevenlabs"` on success), allowing kiosk toasts to indicate engine fallback rather than failing silently (and recorded to `voice_fallback.log`).
   - The engine never sticks in fallback mode; every subsequent utterance retries the configured primary engine.

### 4. Playback Safety & Privacy Rules
1. **Quiet Hours & Mute Switch Delivery Prerequisite**:
   - Quiet hours and a functional software/hardware mute switch must ship and be verified before any unit that plays audio is enabled in production.
2. **Quiet Hours Enforcement**:
   - Suppresses audible speech during configured hours (e.g. `21:00-07:00` for Mike; `23:00-07:00` for Alex). Responses during quiet hours are routed exclusively to display caption toasts on interactive kiosks without emitting sound.
3. **Mute Switch Protection & Visual Indicators**:
   - Physical or software privacy mute status is verified prior to activating audio capture or playback. When muted, wake word detection is halted, the microphone stream is closed at the OS/hardware level, and the display reflects muted status.
   - Active listening must be visibly indicated on screen or hardware LEDs whenever the mic is live per the Listening State Visibility Invariant.
4. **No Unconfirmed Live Tests**:
   - Automated tests and verification scripts must NEVER trigger unprompted audible sound on physical speakers without explicit, prior confirmation from the household owner (Alex / Mike). Test pipelines must mock the ALSA/PipeWire playback sink.

---

## Sentence Streaming (MANDOS)

Mike's Voice Hub (`internal/voice/hub.go`) can play Amos's reply sentence by
sentence, starting well before the full reply is known, when its configured
`brain.url` points at the karakos voice gateway's `POST /ask/stream` (same
request shape as `POST /ask`, in the karakos workspace, not this repo)
instead of `POST /ask`. This is a deployment/config change only — the wire
contract below is purely additive, so pointing `brain.url` back at `/ask`
(or any brain that has never heard of `/ask/stream`) reverts to the
single-chunk behavior above with no other change.

### Capability detection, not a config flag
`BrainClient` gained an optional capability interface rather than a new
field:
```go
type AudioStreamingBrainClient interface {
    BrainClient
    AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error)
}
```
The Hub checks for this via a type assertion on `h.brain`. `DefaultBrainClient`
implements it unconditionally — `AskStreaming` hits the same configured URL as
`Ask`, so if that URL never sends a `sentence` event (a plain `/ask` brain, an
older gateway, a turn with nothing to stream), `onAudio` simply never fires and
the turn behaves exactly like the non-streaming path below. A brain fake in
tests that implements only `Ask` (e.g. `mockBrain`) is untouched and continues
to compile and run unchanged.

### Wire format (`event: sentence`, karakos gateway only)
Additive to the SSE contract above; `/ask` is unchanged.
```
event: sentence
data: {"text": "It's sunny.", "engine": "desktop-tts", "audio_b64": "<base64 WAV>"}
```
- `audio_b64` decodes to raw WAV bytes — the gateway's `synthesize()` always
  returns WAV, so there is no separate `format` field on the wire; the Hub
  hardcodes `Format: "wav"` for every decoded `BrainAudioChunk`.
- `engine` names which TTS engine spoke that sentence (may differ sentence to
  sentence, e.g. one hits the desktop Chatterbox clone and the next falls
  back to Piper mid-turn) — carried through `BrainAudioChunk` but not
  currently forwarded to the edge dock; the `reply` event's `tts_engine`
  stays the Hub's own configured default, as it always has.
- A `sentence` event with a missing or undecodable `audio_b64` still fires
  `onAudio`, with `Data` empty — the caller decides how to handle it (see
  fallback below), it is never silently dropped by the SSE consumer itself.

### Hub behavior when the brain streams sentence audio
- Each `sentence` event becomes one `audio_chunk` SSE event to the edge dock,
  forwarded **immediately** as it arrives (not held back), `chunk_index`
  incrementing from 0, `format: "wav"`, `is_final: false`. The coordinator
  transitions to `StateSpeaking` on the **first** chunk. Forwarding
  immediately (rather than buffering to look ahead for the last one) is the
  point of streaming at all — it's what lets playback start well before the
  full reply is known.
- **Completion marker, not a buffered final chunk**: because the Hub doesn't
  know in advance how many sentences there will be, it can't mark a chunk
  `is_final` at the moment it sends it. Once the brain call returns (and at
  least one real chunk was sent), the Hub emits one more `audio_chunk` with
  empty `data` and `is_final: true` — a pure completion marker, not a
  resend of the last sentence's audio. The edge dock's `_hub_stream_worker`
  already treats an empty/absent `data` as a no-op (`if audio_b64:` before
  decoding+playing), so this marker plays nothing and needs no client
  change.
- **No double speech**: if any sentence audio streamed, the Hub does not also
  call its own `TTSClient.Synthesize` on the full reply text.
- **Text-only sentences (first-class)**: a `sentence` event may carry text
  and no audio. The Hub synthesizes *just that sentence's text* with its own
  `h.tts`. This is a supported mode, not a degraded one: a brain that
  streams text while this instance does speech locally (e.g. Kokoro on an
  Orin) gets the same sentence-level time-to-first-audio as a brain that
  streams pre-rendered audio (the karakos gateway). The same path covers a
  sentence whose audio was missing or undecodable. It logs at debug level. If `h.tts` is unset, or that fallback synthesis also fails,
  the sentence's audio is dropped (logged at warn), but the `reply` event still
  carries the full, un-truncated text for the caption toast.
- **All sentences failed**: if every streamed sentence (and its fallback)
  failed, so zero real chunks were ever sent, the Hub falls back one more
  level — a single `TTSClient.Synthesize` call over the *entire* reply text,
  emitted as one `is_final: true` chunk carrying the audio (not an empty
  marker) — so a turn is never silently mute.
- **Kiosk caption**: the touch kiosk reads `voice.state` from the
  coordinator, not this SSE stream. Each sent chunk transitions the
  coordinator to `StateSpeaking` with `reply` set to the sentences spoken so
  far, so the caption grows sentence by sentence; when the brain call
  returns, `reply` is set to the brain's final text before the completion
  marker.
- **Event ordering changes for a streaming turn only**: `audio_chunk` events
  arrive *during* the brain call (as each sentence completes), and `reply`
  is emitted only once the brain call returns with the final text — the
  reverse of the non-streaming order above (`reply` then a single
  `audio_chunk`). A streaming turn's full sequence is:
  `state → transcript → status* → audio_chunk+ → audio_chunk(marker) →
  reply → done`.
  A non-streaming turn (or a streaming-capable brain that streamed nothing
  this turn) is unchanged: `state → transcript → status* → reply →
  audio_chunk → done`.

### Edge dock playback
`clients/ear/client.py`'s `_hub_stream_worker` already plays every
`audio_chunk` event it receives via a blocking `play_audio()` call as the
event arrives, so N sequential chunks for one turn play back-to-back in
order with no client code change required — `is_final` is informational for
the dock (nothing currently branches on it), not required for correct
playback ordering.

---

## Speaker Identification (Voice Fingerprints)

The Voice Hub can tell who is speaking by matching each utterance against enrolled voice fingerprints. It is optional: with no `speaker_id` block configured, `speaker` is always empty and nothing else changes.

### Speaker is a hint, not authentication
- `speaker` exists to personalise answers (whose calendar, whose list). It is not an identity check. A guest, a child, a recording or a similar voice can clear the threshold, especially on short utterances.
- Brains must not treat `speaker` as proof of identity for anything consequential: sending messages, changing another person's data, purchases, unlocking, or reading private content aloud. For those, confirm by another channel or ask.
- `speaker_score` (best cosine similarity, `0` when matching did not run) is sent alongside so a brain can demand more certainty for writes than for reads.
- An empty `speaker` means "unknown": nobody cleared the threshold, matching is disabled, or matching failed. Brains should answer as the household, not as any individual.
- The `0.70` default threshold is a starting point, not a calibrated value. Each household should tune it against its own enrolled voices and room acoustics. Wake-word-length clips produce less reliable embeddings than multi-second ones.
- The dock's optional `speaker` form field on `POST /api/voice/interact` (above) is not read by Core. Hub-side matching is the only source of `speaker`.

### Matching (implemented in `internal/voice/speaker.go`)
- On every `POST /api/voice/interact`, the Hub embeds the recording and runs STT at the same time. STT stays the critical path: if matching has not finished within 500 ms of STT completing, the turn continues with an empty `speaker`. An STT failure never waits on matching.
- The utterance embedding is compared by cosine similarity against each enrolled fingerprint. The best match at or above `threshold` (default `0.70`) becomes `speaker`; below it, `speaker` is `""`.
- Matching is best-effort. An embed failure, a missing or malformed fingerprint file, or a model mismatch is logged and the turn proceeds with an empty `speaker`. A fingerprint that cannot be compared with the utterance is skipped rather than failing the match for everyone.
- `speaker` is added to the `transcript` SSE event. `node_id`, `speaker` and `speaker_score` are added to the brain request body:
  ```json
  {"prompt": "What's on my calendar?", "session_id": "...", "node_id": "eink-display-livingroom", "speaker": "mike", "speaker_score": 0.83}
  ```

### Embedding endpoint
Embedding runs outside Core, behind the same kind of HTTP endpoint as STT:
```http
POST <embed_url>
Content-Type: audio/wav

<raw 16 kHz mono WAV>

200 {"embedding": [0.012, -0.087, ...], "model": "speechbrain/spkrec-ecapa-voxceleb"}
```

### Fingerprint store
A JSON file on the Hub, one per Hub, re-read whenever it changes on disk so re-enrollment needs no restart. (The rest of the `speaker_id` block, like all of `voice_hub`, is read at startup; changing `embed_url` or `threshold` needs a restart.) All fingerprints must share one dimension and be finite and non-zero, or the file is rejected. Only embeddings are stored, never audio. Sharing fingerprints across multiple Hubs is out of scope. `model` must match the embed endpoint's `model`; embeddings from different models are not comparable.
```json
{
  "model": "speechbrain/spkrec-ecapa-voxceleb",
  "speakers": [
    {"id": "mike",   "embedding": [0.031, -0.112, ...]},
    {"id": "lauren", "embedding": [-0.054, 0.090, ...]}
  ]
}
```

### Configuration
```yaml
voice_hub:
  speaker_id:
    embed_url: "http://<gpu-host>:9099/embed"
    fingerprints_path: "/config/speakers.json"
    threshold: 0.70      # optional, (0, 1]
    timeout_seconds: 5   # optional
```

### Not yet built
Enrollment (the "Add my voice" flow that records prompted phrases and writes the fingerprint file) is a follow-up. Until then the file is produced out of band.

---

## Ambient (Classifier-Gated) Wake Mode — Optional Edge Mode

This is an **opt-in edge mode**, off by default (`wake_mode: openwakeword`). Where it's enabled, it is a deliberate, explicit exception to two invariants stated above:
- The "zero raw audio is transmitted across the LAN until wake verification completes" rule (§1, Ear & Dock Presentation): in `ambient` mode, every VAD-cut speech segment leaves the edge unit and crosses the LAN to the classifier, with no local wake word gating it first.
- The Listening State Visibility Invariant: ambient segments do **not** flash a `listening` HUD state per segment (that would light up the display all day). The `mirrormere-voice` client only relays `listening` once a segment is engaged.

`clients/ear` (`wake_mode` in `config.py` / `voice.yaml`) supports three values:
- **`openwakeword`** (default): unchanged — local wake word only.
- **`ambient`**: no wake word. `openWakeWord` isn't loaded at all (keeps edge CPU low). The end-of-speech VAD (`vad`, `silence_ms` — either the `speech_threshold_db` energy gate or Silero, see "End-of-Speech Detection" below) cuts every speech segment — minimum 0.4s of voiced audio, maximum `max_record_seconds` — and POSTs each one to `ambient_url`. If the response says `engage: true`, the client runs the exact same hub interaction (`POST /api/voice/interact`, per the Streaming Interaction Contract above) that a wake word triggers today, using that same WAV — including the `listening` state relay at the start of that interaction. If `engage: false`, the segment is dropped silently: no hub call, no state relay.
- **`both`**: the wake word still triggers the hub interaction directly, bypassing the classifier; segments that don't start with a wake word fall through to the ambient gate instead.

### Suppression
The client never sends a segment while it is playing a reply, or during `cooldown_seconds` after one ends — otherwise the device's own voice (TTS reply audio) would be captured and re-classified. If `ambient_url` errors or times out (`ambient_timeout_seconds`, default 6s), the client logs at debug and returns to listening; a single classifier failure never crashes or wedges the capture loop, and a timeout does not disable subsequent segments.

### The Ambient Classification Contract (`POST <ambient_url>`)
The endpoint is generic — any service implementing this request/response shape works (the household gateway's own `/ambient` endpoint is one implementation, built separately from this client).

**Request** — `multipart/form-data`:
- `audio`: the captured segment as a 16 kHz mono WAV (same format as `POST /api/voice/interact`'s `audio` field).
- `node_id`: edge dock identifier.

**Response** — `200 application/json`:
```json
{
  "engage": true,
  "transcript": "hey, can you turn on the porch light",
  "score": 0.87,
  "classifier": "keyword-and-intent-v1"
}
```
- `engage`: whether the household brain should act on this segment. Anything other than a literal `true` (missing field, `false`, non-boolean, malformed/non-JSON body, non-2xx status, or a request error/timeout) is treated as "don't engage."
- `transcript`, `score`, `classifier` are informational — logged by the client, not otherwise required by it.

### Configuration
```yaml
ear:
  wake_mode: ambient
  ambient_url: "http://192.168.1.77:9098/ambient"
  ambient_timeout_seconds: 6.0   # optional, default 6.0
```

### End-of-Speech Detection (VAD)
`vad` (`config.py` / `voice.yaml`) selects how the client decides a frame is
speech, used by both the wake-triggered utterance recorder and the ambient
segmenter above:
- **`energy`** (default): the original dBFS gate against `speech_threshold_db`.
- **`silero`**: [Silero VAD](https://github.com/snakers4/silero-vad) scores
  each 512-sample (32ms) window of 16kHz audio via the same ONNX model
  `openWakeWord` already bundles and loads for its own wake-word gating (no
  new dependency); a frame is speech if any window in it scores >=
  `vad_threshold` (default `0.5`). Falls back to `energy` mode with a
  logged warning if the model fails to load at startup.

`silence_ms` still governs the trailing-silence window that ends an
utterance/segment in either mode.
```yaml
ear:
  vad: silero
  vad_threshold: 0.5
```

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
    url: "http://<voice-hub-host>:9000/api/voice/interact" # Jetson Orin Voice Hub LAN address
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
    url: "http://<agent-host>:4000/api/voice/ask"          # Aerial Brain on Ameridroid LAN address
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

### 3. Mike's Ambient E-Ink (`/etc/mirrormere/voice.yaml` on Pi 4B)
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
    url: "http://localhost:9000/api/voice/interact"      # Voice Hub running locally on the Pi 4B
    timeout_seconds: 120

  playback:
    sink: "default"
    quiet_hours: "21:00-07:00"
    duck_video_percent: 20

  display:
    mirrormere_url: "http://localhost:8080"
```

### 4. Mike's Voice Hub (`/etc/voice-hub/config.yaml` on Pi 4B)
```yaml
hub:
  listen: "0.0.0.0:9000"

  stt:
    engine: whisper_http
    url: "http://<desktop-gpu-host>:9099/transcribe"    # Desktop CUDA PC LAN address
    timeout_seconds: 10
    fallback: whisper_local
    whisper_local:
      model: small
      compute_type: int8

  brain:
    adapter: http_agent
    url: "http://<agent-host>:8000/agent/ask"           # Amos Agent API via task queue
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
