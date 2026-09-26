"""
image.py - 1-bit monochrome image validation, 8x8 offline dot stamping, and persistence
Reference: SPEC-009 §4, §Offline Behavior
"""

import logging
import os
import struct
import zlib

logger = logging.getLogger("mirrormere.eink.image")

PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
PANEL_WIDTH = 800
PANEL_HEIGHT = 480
ROW_BYTES = PANEL_WIDTH // 8  # 100 bytes per scanline (1 bit per pixel)


def validate_png(data: bytes) -> tuple[bool, str | None]:
    """
    Validates that data is a valid PNG image formatted for the 800x480 1-bit e-paper panel.
    Returns (is_valid, error_message).
    """
    if not data or len(data) < 33:
        return False, "Payload too short to be a valid PNG"

    if data[:8] != PNG_SIGNATURE:
        return False, "Invalid PNG magic signature"

    # Read IHDR chunk (starts at offset 8: 4 bytes length, 4 bytes tag, 13 bytes data)
    try:
        ihdr_len, ihdr_tag = struct.unpack(">I4s", data[8:16])
        if ihdr_tag != b"IHDR" or ihdr_len != 13:
            return False, f"Expected IHDR chunk, got {ihdr_tag}"

        width, height, bit_depth, color_type, comp, filt, inter = struct.unpack(
            ">IIBBBBB", data[16:29]
        )

        if width != PANEL_WIDTH or height != PANEL_HEIGHT:
            return False, f"Invalid dimensions: expected {PANEL_WIDTH}x{PANEL_HEIGHT}, got {width}x{height}"

        # E-paper display expects 1-bit monochrome (Color Type 0: Grayscale with Bit Depth 1)
        if bit_depth != 1:
            return False, f"Invalid bit depth: expected 1-bit monochrome, got {bit_depth}-bit"

        return True, None
    except Exception as e:
        return False, f"Failed to parse PNG header: {e}"


def stamp_offline_dot(png_bytes: bytes) -> bytes:
    """
    Stamps an 8x8 black square in the top-right corner (x ∈ [792, 799], y ∈ [0, 7])
    over the uncompressed scanlines of an 800x480 1-bit PNG image.
    In 1-bit monochrome: 0 is black, 1 is white.
    Byte 99 of each scanline holds pixels 792..799. Setting byte 99 to 0x00 for rows 0..7
    renders the solid 8x8 black dot.
    """
    is_valid, err = validate_png(png_bytes)
    if not is_valid:
        raise ValueError(f"Cannot stamp dot on invalid PNG: {err}")

    pos = 8
    chunks_before_idat = []
    idat_parts = []
    chunks_after_idat = []
    seen_idat = False

    while pos < len(png_bytes):
        chunk_len, chunk_tag = struct.unpack(">I4s", png_bytes[pos : pos + 8])
        chunk_total_len = 12 + chunk_len
        chunk_raw = png_bytes[pos : pos + chunk_total_len]

        if chunk_tag == b"IDAT":
            seen_idat = True
            idat_parts.append(png_bytes[pos + 8 : pos + 8 + chunk_len])
        elif not seen_idat:
            chunks_before_idat.append(chunk_raw)
        else:
            chunks_after_idat.append(chunk_raw)

        pos += chunk_total_len

    if not idat_parts:
        raise ValueError("Malformed PNG: missing IDAT chunk")

    # Decompress raw scanlines (each row: 1 filter byte + 100 pixel bytes = 101 bytes)
    raw_data = bytearray(zlib.decompress(b"".join(idat_parts)))
    row_stride = 1 + ROW_BYTES  # 101 bytes

    # Stamp 8x8 black dot: rows 0 through 7, pixel columns 792..799 (last byte = index 100)
    for y in range(8):
        offset = y * row_stride + 1 + (ROW_BYTES - 1)  # offset to byte 99
        raw_data[offset] = 0x00  # 8 black pixels

    # Re-compress modified scanlines
    recompressed = zlib.compress(bytes(raw_data), level=6)

    # Construct new IDAT chunk
    idat_crc = zlib.crc32(b"IDAT" + recompressed) & 0xFFFFFFFF
    new_idat = struct.pack(">I4s", len(recompressed), b"IDAT") + recompressed + struct.pack(">I", idat_crc)

    result = [PNG_SIGNATURE]
    result.extend(chunks_before_idat)
    result.append(new_idat)
    result.extend(chunks_after_idat)

    return b"".join(result)


def save_last_image(png_bytes: bytes, target_path: str = "/var/lib/mirrormere-eink/last.png") -> bool:
    """
    Persists the last valid image to disk so a power cycle while offline restores
    the screen without leaving a blank panel. Falls back to user cache or /tmp if
    /var/lib is not writable.
    """
    candidates = [
        target_path,
        os.path.expanduser("~/.cache/mirrormere-eink/last.png"),
        "/tmp/mirrormere-eink/last.png",
    ]

    for path in candidates:
        try:
            parent = os.path.dirname(path)
            if parent and not os.path.exists(parent):
                os.makedirs(parent, exist_ok=True)
            with open(path, "wb") as f:
                f.write(png_bytes)
            logger.info("Persisted last good frame to %s (%d bytes)", path, len(png_bytes))
            return True
        except Exception as e:
            logger.debug("Could not write to %s: %s; trying next candidate", path, e)

    logger.warning("Failed to persist last good frame across all candidate paths")
    return False


def load_last_image(target_path: str = "/var/lib/mirrormere-eink/last.png") -> bytes | None:
    """
    Loads the last persisted image from disk if available.
    """
    candidates = [
        target_path,
        os.path.expanduser("~/.cache/mirrormere-eink/last.png"),
        "/tmp/mirrormere-eink/last.png",
    ]

    for path in candidates:
        if os.path.isfile(path):
            try:
                with open(path, "rb") as f:
                    data = f.read()
                is_valid, _ = validate_png(data)
                if is_valid:
                    logger.info("Loaded persisted frame from %s (%d bytes)", path, len(data))
                    return data
            except Exception as e:
                logger.debug("Could not read frame from %s: %s", path, e)

    return None
