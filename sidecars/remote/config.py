"""Configuration loader and schema for Mirrormere Remote sidecar."""

import os
from dataclasses import dataclass
from typing import List, Optional

try:
    import yaml
except ImportError:
    yaml = None


@dataclass
class RemoteConfig:
    chromecast_host: str = "chromecast.lan"
    cert_dir: str = "/data/certs"
    touch_device: str = "/dev/input/event3"
    http_port: int = 8092
    advert_name: str = "Mirrormere Remote"
    auto_confirm_pairing: bool = True


DEFAULT_CONFIG_PATHS = [
    os.environ.get("CONFIG_PATH", ""),
    "/local/config/remote.yaml",
    "/etc/mirrormere/remote.yaml",
]


def _parse_simple_yaml(text: str) -> dict:
    """Minimal stdlib YAML parser for simple key-value config when PyYAML is unavailable."""
    data = {}
    current_dict = data
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if ":" in stripped:
            k, v = stripped.split(":", 1)
            k = k.strip()
            v = v.strip()
            if indent == 0 and not v:
                sub = {}
                data[k] = sub
                current_dict = sub
            elif not v:
                sub = {}
                current_dict[k] = sub
                current_dict = sub
            else:
                clean_v = v.strip("\"'")
                if clean_v.lower() in ("true", "yes", "on"):
                    current_dict[k] = True
                elif clean_v.lower() in ("false", "no", "off"):
                    current_dict[k] = False
                else:
                    try:
                        current_dict[k] = int(clean_v)
                    except ValueError:
                        current_dict[k] = clean_v
    return data


def load_config(path: Optional[str] = None) -> RemoteConfig:
    """Loads configuration strictly from a scoped YAML file.

    In accordance with system invariants, NO environment variable fallback
    is provided for individual configuration fields.
    """
    cfg = RemoteConfig()

    target_path = path
    if not target_path:
        for p in DEFAULT_CONFIG_PATHS:
            if p and os.path.exists(p):
                target_path = p
                break

    if target_path and os.path.exists(target_path):
        try:
            with open(target_path, "r", encoding="utf-8") as f:
                content = f.read()
                if yaml is not None:
                    raw = yaml.safe_load(content) or {}
                else:
                    raw = _parse_simple_yaml(content)

            # Support nested "remote:" key or flat YAML
            data = raw.get("remote", raw) if isinstance(raw, dict) else {}
            if isinstance(data, dict):
                if "chromecast_host" in data:
                    cfg.chromecast_host = str(data["chromecast_host"])
                elif "chromecast_addr" in data:
                    addr = str(data["chromecast_addr"])
                    cfg.chromecast_host = addr.split(":")[0]
                if "cert_dir" in data:
                    cfg.cert_dir = str(data["cert_dir"])
                if "touch_device" in data:
                    cfg.touch_device = str(data["touch_device"])
                if "http_port" in data:
                    cfg.http_port = int(data["http_port"])
                if "advert_name" in data:
                    cfg.advert_name = str(data["advert_name"])
                if "auto_confirm_pairing" in data:
                    v = data["auto_confirm_pairing"]
                    if isinstance(v, bool):
                        cfg.auto_confirm_pairing = v
                    else:
                        cfg.auto_confirm_pairing = str(v).lower() in ("true", "1", "yes")
        except Exception as e:
            if path:
                raise ValueError(f"Failed to parse config from {path}: {e}") from e

    return cfg
