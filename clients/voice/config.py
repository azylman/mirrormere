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
    wake_models: List[str] = field(default_factory=lambda: ["alexa", "hey_jarvis", "hey_mycroft", "hey_aerial"])
    threshold: float = 0.35
    silence_ms: int = 800
    max_record_seconds: float = 10.0
    speech_threshold_db: float = -31.0
    cooldown_seconds: float = 2.5
    save_path: str = "/tmp/last_utterance.wav"
    audio_device: str = "default"
    sample_rate: int = 16000
    chunk_samples: int = 1280
    models_dir: str = "/opt/mirrormere/voice/models"


DEFAULT_CONFIG_PATH = os.environ.get("MIRRORMERE_VOICE_CONFIG", "/etc/mirrormere/voice.yaml")


def load_config(path: Optional[str] = None) -> VoiceConfig:
    """Loads configuration from YAML file with fallback to environment variables and defaults."""
    cfg = VoiceConfig()
    target_path = path or DEFAULT_CONFIG_PATH

    if os.path.exists(target_path):
        try:
            with open(target_path, "r", encoding="utf-8") as f:
                data = yaml.safe_load(f) or {}
                voice_data = data.get("voice", data)
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

    return cfg
