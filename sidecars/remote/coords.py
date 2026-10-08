"""Coordinate normalization and HID scaling helpers for mirrormere-remote.

Pure Python module with zero external dependencies for fast, hermetic unit testing.
"""

def clamp(val: float, min_val: float, max_val: float) -> float:
    """Clamp value between min_val and max_val."""
    return max(min_val, min(max_val, val))

def normalize_coordinates(x: float, y: float, width: float = 2880.0, height: float = 1620.0) -> tuple[float, float]:
    """Normalize raw touch coordinates into ratios in range [0.0, 1.0]."""
    if width <= 0.0 or height <= 0.0:
        return 0.0, 0.0
    x_ratio = clamp(x / width, 0.0, 1.0)
    y_ratio = clamp(y / height, 0.0, 1.0)
    return x_ratio, y_ratio

def to_hid_digitizer(x_ratio: float, y_ratio: float, max_logical: int = 32767) -> tuple[int, int]:
    """Convert normalized ratios into 16-bit unsigned HID digitizer coordinates."""
    clamped_x = clamp(x_ratio, 0.0, 1.0)
    clamped_y = clamp(y_ratio, 0.0, 1.0)
    return int(clamped_x * max_logical), int(clamped_y * max_logical)

def to_hid_mouse_delta(dx: float, dy: float, scale: float = 1.0) -> tuple[int, int]:
    """Convert relative movement deltas into signed 8-bit HID mouse deltas (-127..127)."""
    scaled_dx = int(dx * scale)
    scaled_dy = int(dy * scale)
    clamped_dx = int(clamp(scaled_dx, -127, 127))
    clamped_dy = int(clamp(scaled_dy, -127, 127))
    return clamped_dx, clamped_dy
