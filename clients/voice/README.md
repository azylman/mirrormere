# Mirrormere Edge Voice Daemon (`mirrormere-voice`)

The edge voice companion daemon for Mirrormere displays and ambient nodes, adhering to **[SPEC-011: Unified Voice Pipeline](../../specs/011-voice-pipeline.md)**.

## Architecture & Responsibilities

`mirrormere-voice` implements the "Dumb Edge Audio Terminal & HUD Presenter" tier of Mirrormere's hub-and-spoke voice architecture:

1. **Ambient Wake Word Detection**: Continuously ingests 16 kHz 16-bit mono PCM from PipeWire/ALSA and runs local openWakeWord neural inference entirely on the edge CPU (<5% CPU load).
2. **State & Event Relay**: Relays interaction states (`listening`, `transcribing`, `thinking`, `speaking`, `idle`, `error`) to Mirrormere Core (`POST /api/voice/state`) to update visual indicators, header transcripts, and caption toasts on interactive touch kiosks (SPEC-010).
3. **Utterance Capture**: Once triggered, records speech through an energy-calibrated VAD gate and saves the resulting audio to a temporary 16 kHz WAV file.
4. **Hub Forwarding**: Forwards captured utterances to the LAN Voice Hub (`POST /api/voice/interact`) for GPU-accelerated STT (Whisper), agent deliberation (Aerial/Amos), and neural TTS (Kokoro/ElevenLabs).
5. **Buffer Flushing & Refractory Cooldown**: Flushes model feature buffers and enforces a refractory cooldown period after recording to eliminate echo re-triggers.

## Configuration

Configuration is loaded from `/etc/mirrormere/voice.yaml` (or via `MIRRORMERE_VOICE_CONFIG`):

```yaml
voice:
  node_id: "touch-kiosk-kitchen"
  mirrormere_url: "http://192.168.1.14/kiosk"
  hub_url: "http://192.168.1.14:9000/api/voice/interact"
  wake_models:
    - "alexa"
    - "hey_jarvis"
    - "hey_aerial"
  threshold: 0.35
  models_dir: "/opt/mirrormere/voice/models"
  silence_ms: 800
  max_record_seconds: 10.0
  speech_threshold_db: -31.0
  cooldown_seconds: 2.5
  audio_device: "default"
```

## Wake Mode

`wake_mode` controls how an utterance gets triggered (see SPEC-011 "Ambient
(Classifier-Gated) Wake Mode" for the full contract):

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
voice:
  wake_mode: ambient
  ambient_url: "http://192.168.1.77:9098/ambient"
```

## Quickstart & Installation

On the edge unit (kiosk mini-PC or Raspberry Pi):

```bash
# Run automated setup:
sudo ./clients/voice/setup.sh

# Start the service:
sudo systemctl start mirrormere-voice.service

# View live telemetry and wake scores:
journalctl -u mirrormere-voice.service -f
```

## Custom Wake Word Models

To add custom wake word models (e.g. `hey_aerial.onnx`):
1. Place the `.onnx` or `.tflite` file into `/opt/mirrormere/voice/models/`.
2. Add the model base name to `wake_models` in `/etc/mirrormere/voice.yaml`.
3. Restart `mirrormere-voice.service`. The daemon auto-scans the directory and loads the model into its active evaluation graph.
