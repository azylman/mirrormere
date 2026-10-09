"""Coordinate normalization and HID scaling helpers for mirrormere-remote.

Pure Python module with zero external dependencies for fast, hermetic unit testing.
"""

from typing import Optional


def clamp(val: float, min_val: float, max_val: float) -> float:
    """Clamp value between min_val and max_val."""
    return max(min_val, min(max_val, val))


def normalize_coordinates(
    x: float,
    y: float,
    width: float = 2880.0,
    height: float = 1620.0,
    min_x: float = 0.0,
    max_x: float = 0.0,
    min_y: float = 0.0,
    max_y: float = 0.0,
) -> tuple[float, float]:
    """Normalize raw touch coordinates into ratios in range [0.0, 1.0].

    Supports either explicit min/max calibration bounds from device absinfo,
    or legacy width/height dimension scaling.
    """
    x_range = (max_x - min_x) if max_x > min_x else width
    y_range = (max_y - min_y) if max_y > min_y else height
    if x_range <= 0.0 or y_range <= 0.0:
        return 0.0, 0.0
    x_offset = min_x if max_x > min_x else 0.0
    y_offset = min_y if max_y > min_y else 0.0
    x_ratio = clamp((x - x_offset) / x_range, 0.0, 1.0)
    y_ratio = clamp((y - y_offset) / y_range, 0.0, 1.0)
    return x_ratio, y_ratio


def to_hid_digitizer(
    x_ratio: float,
    y_ratio: float,
    max_logical_x: int = 32767,
    max_logical_y: int = 18431,
    max_logical: Optional[int] = None,
) -> tuple[int, int]:
    """Convert normalized ratios into 16-bit unsigned HID digitizer coordinates.

    Defaults to a 16:9 widescreen logical aspect ratio (32768 x 18432) so Android's
    TouchInputMapper applies uniform scaling in POINTER mode without stretching Y.
    """
    if max_logical is not None:
        max_logical_x = max_logical
        max_logical_y = max_logical
    clamped_x = clamp(x_ratio, 0.0, 1.0)
    clamped_y = clamp(y_ratio, 0.0, 1.0)
    return int(clamped_x * max_logical_x), int(clamped_y * max_logical_y)


def to_hid_mouse_delta(dx: float, dy: float, scale: float = 1.0) -> tuple[int, int]:
    """Convert relative movement deltas into signed 8-bit HID mouse deltas (-127..127)."""
    scaled_dx = int(dx * scale)
    scaled_dy = int(dy * scale)
    clamped_dx = int(clamp(scaled_dx, -127, 127))
    clamped_dy = int(clamp(scaled_dy, -127, 127))
    return clamped_dx, clamped_dy
