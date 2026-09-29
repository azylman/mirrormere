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
