"""
config.py - Configuration loader and schemas for Mirrormere E-Ink display node
Reference: SPEC-009 §Configuration
"""

from dataclasses import dataclass, field
import os

try:
    import yaml
except ImportError:
    yaml = None


@dataclass
class ServerConfig:
    events_url: str = "http://localhost:8080/api/events"
    image_url: str = "http://localhost:8081/eink.png"


@dataclass
class PanelPinsConfig:
    rst: int = 27
    dc: int = 22
    busy: int = 17
    cs: int = 8
    pwr: int | None = None


@dataclass
class PanelConfig:
    driver: str = "epd7in5_V2"
    pins: PanelPinsConfig = field(default_factory=PanelPinsConfig)


@dataclass
class RefreshConfig:
    coalesce_seconds: float = 5.0
    min_refresh_seconds: float = 60.0
    clock_refresh_seconds: float = 300.0
    max_idle_seconds: float = 900.0
    full_refresh_minutes: float = 60.0
    max_consecutive_partials: int = 30
    offline_grace_seconds: float = 120.0
    clear_on_shutdown: bool = False


@dataclass
class ButtonsConfig:
    enabled: bool = False
    refresh_pin: int = 5
    next_screen_pin: int = 6


@dataclass
class EinkConfig:
    server: ServerConfig = field(default_factory=ServerConfig)
    panel: PanelConfig = field(default_factory=PanelConfig)
    refresh: RefreshConfig = field(default_factory=RefreshConfig)
    buttons: ButtonsConfig = field(default_factory=ButtonsConfig)


def _parse_yaml_value(val_str: str):
    val = val_str.strip()
    if val.startswith('"') and val.endswith('"') and len(val) >= 2:
        return val[1:-1]
    if val.startswith("'") and val.endswith("'") and len(val) >= 2:
        return val[1:-1]
    low = val.lower()
    if low in ("true", "yes", "on"):
        return True
    if low in ("false", "no", "off"):
        return False
    if low in ("null", "none", "~", ""):
        return None
    try:
        if "." in val:
            return float(val)
        return int(val)
    except ValueError:
        return val


def _simple_yaml_parse(filepath: str) -> dict:
    """
    Lightweight fallback parser for basic YAML config files when PyYAML is unavailable.
    Handles sections and 2-space indented key-value mappings.
    """
    data: dict = {}
    current_section: str | None = None
    sub_section: str | None = None

    try:
        with open(filepath, "r", encoding="utf-8") as f:
            for line in f:
                stripped = line.strip()
                if not stripped or stripped.startswith("#"):
                    continue

                indent = len(line) - len(line.lstrip(" "))

                if ":" in stripped:
                    key, _, val = stripped.partition(":")
                    key = key.strip()
                    val = val.strip()

                    if indent == 0:
                        current_section = key
                        sub_section = None
                        if current_section not in data:
                            data[current_section] = {}
                    elif indent in (2, 4):
                        if not val:
                            sub_section = key
                            if current_section:
                                if sub_section not in data[current_section]:
                                    data[current_section][sub_section] = {}
                        else:
                            parsed_val = _parse_yaml_value(val)
                            if sub_section and current_section:
                                data[current_section][sub_section][key] = parsed_val
                            elif current_section:
                                data[current_section][key] = parsed_val
    except Exception:
        return {}

    return data


def load_config(path: str | None = None) -> EinkConfig:
    """
    Loads configuration from a YAML file, merging with default values.
    Also honors environment variable overrides:
      - EVENTS_URL / MIRRORMERE_EVENTS_URL
      - IMAGE_URL / MIRRORMERE_IMAGE_URL
    """
    cfg_data: dict = {}

    target_path = path or os.environ.get("MIRRORMERE_EINK_CONFIG")
    if not target_path:
        for candidate in ["/etc/mirrormere-eink/config.yaml", "config.yaml"]:
            if os.path.isfile(candidate):
                target_path = candidate
                break

    if target_path and os.path.isfile(target_path):
        if yaml is not None:
            try:
                with open(target_path, "r", encoding="utf-8") as f:
                    loaded = yaml.safe_load(f)
                    if isinstance(loaded, dict):
                        cfg_data = loaded
            except Exception:
                cfg_data = {}
        else:
            cfg_data = _simple_yaml_parse(target_path)

    server_data = cfg_data.get("server", {})
    panel_data = cfg_data.get("panel", {})
    pins_data = panel_data.get("pins", {})
    refresh_data = cfg_data.get("refresh", {})
    buttons_data = cfg_data.get("buttons", {})

    server_cfg = ServerConfig(
        events_url=str(
            server_data.get("events_url")
            or os.environ.get("MIRRORMERE_EVENTS_URL")
            or os.environ.get("EVENTS_URL")
            or "http://localhost:8080/api/events"
        ),
        image_url=str(
            server_data.get("image_url")
            or os.environ.get("MIRRORMERE_IMAGE_URL")
            or os.environ.get("IMAGE_URL")
            or "http://localhost:8081/eink.png"
        ),
    )

    pins_cfg = PanelPinsConfig(
        rst=int(pins_data.get("rst", 27)),
        dc=int(pins_data.get("dc", 22)),
        busy=int(pins_data.get("busy", 17)),
        cs=int(pins_data.get("cs", 8)),
        pwr=int(pins_data["pwr"]) if pins_data.get("pwr") is not None else None,
    )

    panel_cfg = PanelConfig(
        driver=str(panel_data.get("driver", "epd7in5_V2")),
        pins=pins_cfg,
    )

    refresh_cfg = RefreshConfig(
        coalesce_seconds=float(refresh_data.get("coalesce_seconds", 5.0)),
        min_refresh_seconds=float(refresh_data.get("min_refresh_seconds", 60.0)),
        clock_refresh_seconds=float(refresh_data.get("clock_refresh_seconds", 300.0)),
        max_idle_seconds=float(refresh_data.get("max_idle_seconds", 900.0)),
        full_refresh_minutes=float(refresh_data.get("full_refresh_minutes", 60.0)),
        max_consecutive_partials=int(refresh_data.get("max_consecutive_partials", 30)),
        offline_grace_seconds=float(refresh_data.get("offline_grace_seconds", 120.0)),
        clear_on_shutdown=bool(refresh_data.get("clear_on_shutdown", False)),
    )

    buttons_cfg = ButtonsConfig(
        enabled=bool(buttons_data.get("enabled", False)),
        refresh_pin=int(buttons_data.get("refresh_pin", 5)),
        next_screen_pin=int(buttons_data.get("next_screen_pin", 6)),
    )

    return EinkConfig(
        server=server_cfg,
        panel=panel_cfg,
        refresh=refresh_cfg,
        buttons=buttons_cfg,
    )
