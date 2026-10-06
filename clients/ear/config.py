"""Configuration loader and schema for Mirrormere Edge Voice Daemon."""

import os
from dataclasses import dataclass, field
from typing import List, Optional

try:
    import yaml
except ImportError:
    yaml = None


@dataclass
class VoiceConfig:
    node_id: str = "touch-kiosk-kitchen"
    mirrormere_url: str = "http://192.168.1.14/kiosk"
    hub_url: str = "http://192.168.1.14:9000/api/voice/interact"
    wake_models: List[str] = field(default_factory=lambda: ["hey_aerial"])
    threshold: float = 0.35
    silence_ms: int = 400
    max_record_seconds: float = 10.0
    speech_threshold_db: float = -31.0
    cooldown_seconds: float = 2.5
    save_path: str = "/tmp/last_utterance.wav"
    audio_device: str = "default"
    sample_rate: int = 16000
    chunk_samples: int = 1280
    models_dir: str = "/opt/mirrormere/voice/models"
    # Wake mode: "openwakeword" (default, today's behavior), "ambient" (no
    # wake word - every VAD-cut speech segment is classified by ambient_url),
    # or "both" (wake word triggers directly; segments without one go
    # through the ambient gate).
    wake_mode: str = "openwakeword"
    ambient_url: str = ""
    ambient_timeout_seconds: float = 6.0
    # End-of-speech detection: "energy" (default, today's dBFS gate against
    # speech_threshold_db) or "silero" (Silero VAD neural speech
    # probability, reusing openWakeWord's bundled ONNX model). silence_ms
    # still governs the trailing-silence window in either mode.
    vad: str = "energy"
    # Silero speech-probability threshold (0.0-1.0) at or above which a
    # window counts as speech. Only used when vad == "silero".
    vad_threshold: float = 0.5


DEFAULT_CONFIG_PATH = os.environ.get("MIRRORMERE_VOICE_CONFIG", "/etc/mirrormere/voice.yaml")


def _parse_simple_yaml(text: str) -> dict:
    data = {}
    current_section = data
    current_list = None
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if stripped.startswith("- ") and current_list is not None:
            current_list.append(stripped[2:].strip().strip("\"'"))
            continue
        current_list = None
        if ":" in stripped:
            k, v = stripped.split(":", 1)
            k = k.strip()
            v = v.strip()
            if indent == 0 and not v:
                sub = {}
                data[k] = sub
                current_section = sub
            elif not v:
                sub_list = []
                current_section[k] = sub_list
                current_list = sub_list
            else:
                clean_v = v.strip("\"'")
                try:
                    if "." in clean_v:
                        parsed_v = float(clean_v)
                    else:
                        parsed_v = int(clean_v)
                except ValueError:
                    parsed_v = clean_v
                current_section[k] = parsed_v
    return data


def load_config(path: Optional[str] = None) -> VoiceConfig:
    """Loads configuration from YAML file with fallback to environment variables and defaults."""
    cfg = VoiceConfig()
    target_path = path or DEFAULT_CONFIG_PATH

    if os.path.exists(target_path):
        try:
            with open(target_path, "r", encoding="utf-8") as f:
                if yaml is not None:
                    data = yaml.safe_load(f) or {}
                else:
                    data = _parse_simple_yaml(f.read())
                # Top-level key: "ear" is the preferred name (the client is
                # "the ear"); "voice" is kept for existing deployed configs.
                # If both are present, "ear" wins.
                if isinstance(data.get("ear"), dict):
                    voice_data = data["ear"]
                elif isinstance(data.get("voice"), dict):
                    voice_data = data["voice"]
                else:
                    voice_data = data
                if isinstance(voice_data, dict):
                    for k, v in voice_data.items():
                        if hasattr(cfg, k):
                            field_type = type(getattr(cfg, k))
                            if field_type == float and isinstance(v, (int, float)):
                                setattr(cfg, k, float(v))
                            elif field_type == int and isinstance(v, int):
                                setattr(cfg, k, int(v))
                            elif field_type == list and isinstance(v, list):
                                setattr(cfg, k, [str(item) for item in v])
                            elif field_type == str and isinstance(v, str):
                                setattr(cfg, k, str(v))
        except Exception:
            # Fall back safely to defaults on parse error
            pass

    # Environment overrides
    if "MIRRORMERE_NODE_ID" in os.environ:
        cfg.node_id = os.environ["MIRRORMERE_NODE_ID"]
    if "MIRRORMERE_URL" in os.environ:
        cfg.mirrormere_url = os.environ["MIRRORMERE_URL"]
    if "MIRRORMERE_HUB_URL" in os.environ:
        cfg.hub_url = os.environ["MIRRORMERE_HUB_URL"]
    if "MIRRORMERE_AUDIO_DEVICE" in os.environ:
        cfg.audio_device = os.environ["MIRRORMERE_AUDIO_DEVICE"]
    if "MIRRORMERE_SILENCE_MS" in os.environ:
        try:
            cfg.silence_ms = int(os.environ["MIRRORMERE_SILENCE_MS"])
        except ValueError:
            pass
    if "MIRRORMERE_VAD" in os.environ:
        cfg.vad = os.environ["MIRRORMERE_VAD"]
    if "MIRRORMERE_VAD_THRESHOLD" in os.environ:
        try:
            cfg.vad_threshold = float(os.environ["MIRRORMERE_VAD_THRESHOLD"])
        except ValueError:
            pass

    _validate(cfg)
    return cfg


def _validate(cfg: VoiceConfig) -> None:
    """Clamps/normalizes fields that can't be trusted to arbitrary YAML input."""
    if cfg.vad not in ("energy", "silero"):
        cfg.vad = "energy"
    if not isinstance(cfg.vad_threshold, (int, float)) or not (0.0 <= float(cfg.vad_threshold) <= 1.0):
        cfg.vad_threshold = 0.5
    else:
        cfg.vad_threshold = float(cfg.vad_threshold)
