"""Configuration loader and schema for Mirrormere Remote sidecar."""

import os
from dataclasses import dataclass, field
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
    enable_evdev: bool = False
    bluetooth_keepalive: bool = True
    bluetooth_keepalive_interval: float = 10.0
    advert_min_interval: int = 30
    advert_max_interval: int = 50
    tap_duration: float = 0.015
    bluetooth_heartbeat: bool = True
    bluetooth_heartbeat_interval: float = 30.0
    managed_devices: List[str] = field(default_factory=list)
    pointer_mode: str = "touch"

    def __post_init__(self):
        if self.managed_devices:
            self.managed_devices = [m.strip().upper() for m in self.managed_devices if m.strip()]
        if self.pointer_mode:
            self.pointer_mode = self.pointer_mode.strip().lower()


DEFAULT_CONFIG_PATHS = [
    os.environ.get("CONFIG_PATH", ""),
    "/local/config/remote.yaml",
    "/etc/mirrormere/remote.yaml",
]


def _parse_simple_yaml(text: str) -> dict:
    """Minimal stdlib YAML parser for simple key-value and list config when PyYAML is unavailable."""
    data = {}
    stack = [(-1, data)]
    current_key = None
    parent_dict = data
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())

        while len(stack) > 1 and indent <= stack[-1][0]:
            stack.pop()

        current_dict = stack[-1][1]

        if stripped.startswith("- "):
            item = stripped[2:].strip().strip("\"'")
            if current_key and isinstance(parent_dict.get(current_key), list):
                parent_dict[current_key].append(item)
            elif current_key and current_key in parent_dict:
                parent_dict[current_key] = [item]
            continue

        if ":" in stripped:
            k, v = stripped.split(":", 1)
            k = k.strip()
            v = v.strip()
            current_key = k
            parent_dict = current_dict
            if not v:
                sub = {}
                current_dict[k] = sub
                stack.append((indent, sub))
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
                        try:
                            current_dict[k] = float(clean_v)
                        except ValueError:
                            current_dict[k] = clean_v
    return data


def load_config(path: Optional[str] = None) -> RemoteConfig:
    """Loads configuration strictly from a scoped YAML file.

    In accordance with system invariants, NO environment variable fallback
    is provided for individual configuration fields, except where explicitly
    permitted (MIRRORMERE_REMOTE_MANAGED_DEVICES, MIRRORMERE_REMOTE_POINTER_MODE).
    """
    cfg = RemoteConfig()

    env_managed = os.environ.get("MIRRORMERE_REMOTE_MANAGED_DEVICES")
    if env_managed:
        cfg.managed_devices = [m.strip().upper() for m in env_managed.split(",") if m.strip()]

    env_pointer = os.environ.get("MIRRORMERE_REMOTE_POINTER_MODE")
    if env_pointer:
        cfg.pointer_mode = env_pointer.strip().lower()

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
                if "enable_evdev" in data:
                    v = data["enable_evdev"]
                    if isinstance(v, bool):
                        cfg.enable_evdev = v
                    else:
                        cfg.enable_evdev = str(v).lower() in ("true", "1", "yes")
                if "bluetooth_keepalive" in data:
                    v = data["bluetooth_keepalive"]
                    if isinstance(v, bool):
                        cfg.bluetooth_keepalive = v
                    else:
                        cfg.bluetooth_keepalive = str(v).lower() in ("true", "1", "yes")
                if "bluetooth_keepalive_interval" in data:
                    try:
                        cfg.bluetooth_keepalive_interval = float(data["bluetooth_keepalive_interval"])
                    except ValueError:
                        pass
                if "advert_min_interval" in data:
                    try:
                        cfg.advert_min_interval = int(data["advert_min_interval"])
                    except ValueError:
                        pass
                if "advert_max_interval" in data:
                    try:
                        cfg.advert_max_interval = int(data["advert_max_interval"])
                    except ValueError:
                        pass
                if "tap_duration" in data:
                    try:
                        cfg.tap_duration = float(data["tap_duration"])
                    except ValueError:
                        pass
                if "bluetooth_heartbeat" in data:
                    v = data["bluetooth_heartbeat"]
                    if isinstance(v, bool):
                        cfg.bluetooth_heartbeat = v
                    else:
                        cfg.bluetooth_heartbeat = str(v).lower() in ("true", "1", "yes")
                if "bluetooth_heartbeat_interval" in data:
                    try:
                        cfg.bluetooth_heartbeat_interval = float(data["bluetooth_heartbeat_interval"])
                    except ValueError:
                        pass
                if "managed_devices" in data:
                    v = data["managed_devices"]
                    if isinstance(v, list):
                        cfg.managed_devices = [str(m).strip().upper() for m in v if str(m).strip()]
                    elif isinstance(v, str):
                        cfg.managed_devices = [m.strip().strip("'\"").upper() for m in v.strip("[]").split(",") if m.strip()]
                if "pointer_mode" in data:
                    cfg.pointer_mode = str(data["pointer_mode"]).strip().lower()
        except Exception as e:
            if path:
                raise ValueError(f"Failed to parse config from {path}: {e}") from e

    return cfg
