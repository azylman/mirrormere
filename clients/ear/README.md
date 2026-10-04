# The Ear — Mirrormere Edge Voice Daemon (`mirrormere-voice`)

"The ear" is the edge voice companion daemon for Mirrormere displays and ambient nodes, adhering to **[: Unified Voice Pipeline](../../)**.

## Architecture & Responsibilities

The ear implements the "Dumb Edge Audio Terminal & HUD Presenter" tier of Mirrormere's hub-and-spoke voice architecture:

1. **Ambient Wake Word Detection**: Continuously ingests 16 kHz 16-bit mono PCM from PipeWire/ALSA and runs local openWakeWord neural inference entirely on the edge CPU (<5% CPU load).
2. **State & Event Relay**: Relays interaction states (`listening`, `transcribing`, `thinking`, `speaking`, `idle`, `error`) to Mirrormere Core (`POST /api/voice/state`) to update visual indicators, header transcripts, and caption toasts on interactive touch kiosks.
3. **Utterance Capture**: Once triggered, records speech through an energy-calibrated VAD gate and saves the resulting audio to a temporary 16 kHz WAV file.
4. **Hub Forwarding**: Forwards captured utterances to the LAN Voice Hub (`POST /api/voice/interact`) for GPU-accelerated STT (Whisper), agent deliberation (Aerial/Amos), and neural TTS (Kokoro/ElevenLabs).
5. **Buffer Flushing & Refractory Cooldown**: Flushes model feature buffers and enforces a refractory cooldown period after recording to eliminate echo re-triggers.

## Configuration

Configuration is loaded from `/etc/mirrormere/voice.yaml` (or via `MIRRORMERE_VOICE_CONFIG`).
The top-level key is `ear:` (preferred; if a config carries both `ear:` and
the older `voice:` key, `ear:` wins). `voice:` is still accepted, unchanged,
for existing deployed configs:

```yaml
ear:
  node_id: "touch-kiosk-kitchen"
  mirrormere_url: "http://192.168.1.14/kiosk"
  hub_url: "http://192.168.1.14:9000/api/voice/interact"
  wake_models:
    - "alexa"
    - "hey_jarvis"
    - "hey_aerial"
  threshold: 0.35
  models_dir: "/opt/mirrormere/voice/models"
  silence_ms: 400
  max_record_seconds: 10.0
  speech_threshold_db: -31.0
  cooldown_seconds: 2.5
  audio_device: "default"
```

## End-of-Speech Detection (VAD)

`vad` controls how the daemon decides whether a given audio frame is speech,
both for the wake-triggered utterance recorder and the ambient segmenter:

- **`energy`** (default): the original dBFS energy gate against
  `speech_threshold_db`. Unchanged behavior.
- **`silero`**: [Silero VAD](https://github.com/snakers4/silero-vad), a small
  recurrent neural model, scores each 512-sample (32ms) window of 16kHz
  audio; a frame counts as speech if any window in it scores >=
  `vad_threshold` (default `0.5`). It reuses the same ONNX model
  `openwakeword` already bundles and loads for its own wake-word gating (no
  new dependency). If the model fails to load at startup, the daemon logs a
  warning and falls back to `energy` mode rather than crashing.

`silence_ms` still governs the trailing-silence window that ends an
utterance/segment in either mode. Per-window Silero inference time (mean ms)
is included in the 10s heartbeat log line.

```yaml
ear:
  vad: "silero"
  vad_threshold: 0.5
```

## Wake Mode

`wake_mode` controls how an utterance gets triggered Wake Mode" for the full contract):

- **`openwakeword`** (default): today's behavior, unchanged. `openWakeWord`
  runs locally and a wake phrase starts recording.
- **`ambient`**: no wake word at all. `openWakeWord` isn't even loaded (keeps
  CPU low). The existing energy VAD cuts every speech segment (minimum 0.4s,
  maximum `max_record_seconds`) and POSTs it as multipart form data (`audio`
  = 16 kHz mono WAV, `node_id`) to `ambient_url`. The response must be
  `{"engage": bool, "transcript": str, "score": float, "classifier": str}`.
  When `engage` is true, the daemon runs the exact same hub interaction a
  wake word triggers today, on that same WAV, including the `listening`
  state relay. When `engage` is false, the segment is dropped silently -
  nothing is relayed and the HUD doesn't flash.
- **`both`**: the wake word still triggers directly and bypasses the
  ambient classifier; segments that don't start with a wake word go through
  the ambient gate instead.

Segments are never sent while the device is playing a reply, or during
`cooldown_seconds` after one ends, so the daemon doesn't classify (or
engage on) its own voice. If `ambient_url` errors or times out
(`ambient_timeout_seconds`, default 6s), the failure is logged at debug and
capture continues uninterrupted.

```yaml
ear:
  wake_mode: ambient
  ambient_url: "http://192.168.1.77:9098/ambient"
```

## Quickstart & Installation

### Option 1: Docker Container (Recommended for Touch Kiosk / N100)

Pull the pre-built multi-architecture container image from GitHub Container Registry:

```bash
docker pull ghcr.io/azylman/mirrormere-ear:latest
```

Run as part of the edge kiosk compose stack (`deploy/compose.client.yaml`):

```bash
docker compose -f deploy/compose.client.yaml up -d mirrormere-ear
```

Or run directly with ALSA capture and PipeWire PulseAudio socket passthrough:

```bash
docker run -d \
  --name mirrormere-ear \
  --restart unless-stopped \
  --network host \
  --read-only \
  --tmpfs /tmp \
  --security-opt no-new-privileges:true \
  --ulimit rtprio=99 \
  --ulimit memlock=-1 \
  --device /dev/snd:/dev/snd \
  --group-add audio \
  -e PULSE_SERVER=unix:/run/user/1000/pulse/native \
  -v /run/user/1000/pulse/native:/run/user/1000/pulse/native:ro \
  -v /etc/mirrormere/voice.yaml:/config/voice.yaml:ro \
  -v /opt/mirrormere/voice/models:/opt/mirrormere/voice/models \
  ghcr.io/azylman/mirrormere-ear:latest
```

### Option 2: Systemd Service (Bare Metal on Debian / Raspberry Pi)

On the edge unit (kiosk mini-PC or Raspberry Pi):

```bash
# Run automated setup:
sudo ./clients/ear/setup.sh

# Start the service:
sudo systemctl start mirrormere-voice.service

# View live telemetry and wake scores:
journalctl -u mirrormere-voice.service -f
```

## Custom Wake Word Models

To add custom wake word models (e.g. `hey_aerial.onnx`):
1. Place the `.onnx` or `.tflite` file into `/opt/mirrormere/voice/models/`.
2. Add the model base name to `wake_models` in `/etc/mirrormere/voice.yaml`.
3. Restart `mirrormere-voice.service` (or restart the `mirrormere-ear` container). The daemon auto-scans the directory and loads the model into its active evaluation graph.
