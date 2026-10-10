"""Keycode translation and mapping dictionaries for mirrormere-remote.

Pure Python module with zero external dependencies for fast, hermetic unit testing.
"""

from typing import Optional, Tuple

# USB HID Consumer Usage Page (0x0C) codes
CONSUMER_KEYCODES: dict[str, int] = {
    "back": 0x0224,      # AC Back
    "home": 0x0223,      # AC Home
    "play": 0x00CD,      # Play/Pause
    "pause": 0x00CD,     # Play/Pause
    "playpause": 0x00CD, # Play/Pause
    "volup": 0x00E9,     # Volume Increment
    "volume_up": 0x00E9,
    "voldown": 0x00EA,   # Volume Decrement
    "volume_down": 0x00EA,
    "mute": 0x00E2,      # Mute
    "power": 0x0030,     # Power
}

# Android TV Remote Protobuf Keycodes
REMOTE_KEYCODES: dict[str, str] = {
    "up": "DPAD_UP",
    "down": "DPAD_DOWN",
    "left": "DPAD_LEFT",
    "right": "DPAD_RIGHT",
    "center": "DPAD_CENTER",
    "select": "DPAD_CENTER",
    "enter": "DPAD_CENTER",
    "ok": "DPAD_CENTER",
    "back": "BACK",
    "home": "HOME",
    "play": "MEDIA_PLAY_PAUSE",
    "pause": "MEDIA_PLAY_PAUSE",
    "volup": "VOLUME_UP",
    "voldown": "VOLUME_DOWN",
    "mute": "VOLUME_MUTE",
    "dpad_up": "DPAD_UP",
    "dpad_down": "DPAD_DOWN",
    "dpad_left": "DPAD_LEFT",
    "dpad_right": "DPAD_RIGHT",
    "dpad_center": "DPAD_CENTER",
    "power": "POWER",
    "wake": "WAKE",
    "wakeup": "WAKE",
}

def lookup_consumer_key(name: str) -> Optional[int]:
    """Look up HID Consumer Control keycode (case-insensitive)."""
    clean = name.strip().lower()
    return CONSUMER_KEYCODES.get(clean)

def lookup_remote_key(name: str) -> Optional[str]:
    """Look up Android TV Remote key command string (case-insensitive)."""
    clean = name.strip().lower()
    return REMOTE_KEYCODES.get(clean)

def resolve_key_dispatch(name: str) -> Tuple[Optional[int], Optional[str]]:
    """Resolve key name into both HID consumer code and Android TV Remote key string."""
    clean = name.strip().lower()
    hid_code = CONSUMER_KEYCODES.get(clean)
    remote_cmd = REMOTE_KEYCODES.get(clean)
    return hid_code, remote_cmd
