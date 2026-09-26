"""
test_panel.py - Hermetic unit tests for SPI panel manager, pin patching, offline guard, and hardware buttons
Reference: SPEC-009 §4-§8, Issue #247
"""

import json
import os
import struct
import tempfile
import time
import unittest
import urllib.request
import zlib

from config import EinkConfig, PanelPinsConfig
from image import validate_png, stamp_offline_dot, save_last_image, load_last_image
from panel import FakePanel, PanelManager, patch_bonnet_pins
from offline import OfflineGuard
from buttons import ButtonHandler
from health import HealthServer


def make_test_png(width: int = 800, height: int = 480, bit_depth: int = 1, fill_byte: int = 0xFF) -> bytes:
    """Creates a valid 1-bit PNG test buffer."""
    row_bytes = width // 8
    raw = bytearray()
    for _ in range(height):
        raw.append(0)  # Filter byte None
        raw.extend([fill_byte] * row_bytes)

    def chunk(tag: bytes, data: bytes) -> bytes:
        crc = zlib.crc32(tag + data) & 0xFFFFFFFF
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", crc)

    ihdr = struct.pack(">IIBBBBB", width, height, bit_depth, 0, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(bytes(raw))) + chunk(b"IEND", b"")


class MockClock:
    def __init__(self, start: float = 1000.0):
        self.val = start

    def time(self) -> float:
        return self.val

    def advance(self, secs: float):
        self.val += secs


class TestEinkPanelAndHardware(unittest.TestCase):
    def setUp(self):
        self.clock = MockClock()
        self.panel = FakePanel()
        self.config = EinkConfig()
        self.manager = PanelManager(self.config, panel=self.panel, time_fn=self.clock.time)

    def test_bonnet_pin_patching(self):
        """
        Verifies that patch_bonnet_pins overrides driver module constants with
        the Adafruit Bonnet pin mapping (RST=27, DC=22, BUSY=17, CS=8, PWR=None).
        """
        class MockEPDConfig:
            RST_PIN = 17
            DC_PIN = 25
            BUSY_PIN = 24
            CS_PIN = 8
            PWR_PIN = 18

        pins = PanelPinsConfig(rst=27, dc=22, busy=17, cs=8, pwr=None)
        patch_bonnet_pins(pins, MockEPDConfig)

        self.assertEqual(MockEPDConfig.RST_PIN, 27)
        self.assertEqual(MockEPDConfig.DC_PIN, 22)
        self.assertEqual(MockEPDConfig.BUSY_PIN, 17)
        self.assertEqual(MockEPDConfig.CS_PIN, 8)
        self.assertIsNone(MockEPDConfig.PWR_PIN)

    def test_image_validation(self):
        """
        Verifies input validation rejects malformed PNGs, invalid dimensions, or non-1-bit payloads.
        """
        valid_png = make_test_png(800, 480, 1)
        ok, err = validate_png(valid_png)
        self.assertTrue(ok)
        self.assertIsNone(err)

        # Invalid magic signature
        ok, err = validate_png(b"not-a-png-file-at-all-abcdef1234567890")
        self.assertFalse(ok)
        self.assertIn("signature", err)

        # Wrong dimensions (e.g. 640x480)
        wrong_size = make_test_png(640, 480, 1)
        ok, err = validate_png(wrong_size)
        self.assertFalse(ok)
        self.assertIn("dimensions", err)

        # Wrong bit depth (e.g. 8-bit)
        wrong_depth = make_test_png(800, 480, 8)
        ok, err = validate_png(wrong_depth)
        self.assertFalse(ok)
        self.assertIn("bit depth", err)

        # Manager rejects invalid frame before SPI
        status = self.manager.write_frame(wrong_size)
        self.assertEqual(status, "rejected")
        self.assertEqual(len(self.panel.frames), 0)

    def test_refresh_lifecycle_and_deep_sleep(self):
        """
        Verifies the refresh lifecycle:
          - Initial write is always FULL refresh
          - Next 30 writes are PARTIAL refreshes
          - 31st write triggers FULL refresh (max_consecutive_partials = 30)
          - Deep sleep called after every single write
        """
        frame = make_test_png(800, 480, 1)

        # 1. Initial write -> Full refresh
        type1 = self.manager.write_frame(frame)
        self.assertEqual(type1, "full")
        self.assertEqual(self.panel.full_refreshes, 1)
        self.assertEqual(self.panel.partial_refreshes, 0)
        self.assertEqual(self.panel.sleeps, 1, "Must enter deep sleep immediately after write")
        self.assertEqual(self.manager.consecutive_partials, 0)

        # 2. Next 30 writes -> Partial refreshes
        for i in range(1, 31):
            self.clock.advance(10.0)
            res_type = self.manager.write_frame(frame)
            self.assertEqual(res_type, "partial")
            self.assertEqual(self.panel.partial_refreshes, i)
            self.assertEqual(self.manager.consecutive_partials, i)
            self.assertEqual(self.panel.sleeps, 1 + i, "Deep sleep required after every partial")

        # 3. 31st write -> Full refresh (limit reached)
        self.clock.advance(10.0)
        type_31 = self.manager.write_frame(frame)
        self.assertEqual(type_31, "full")
        self.assertEqual(self.panel.full_refreshes, 2)
        self.assertEqual(self.manager.consecutive_partials, 0, "Counter must reset after full refresh")
        self.assertEqual(self.panel.sleeps, 32)

    def test_full_refresh_interval_timer(self):
        """
        Verifies that exceeding full_refresh_minutes (default 60m) forces a full refresh.
        """
        frame = make_test_png(800, 480, 1)
        self.manager.write_frame(frame)  # initial full refresh at t=1000
        self.assertEqual(self.panel.full_refreshes, 1)

        # Write 5 partials
        for _ in range(5):
            self.clock.advance(60.0)
            self.manager.write_frame(frame)
        self.assertEqual(self.panel.full_refreshes, 1)
        self.assertEqual(self.panel.partial_refreshes, 5)

        # Advance clock by 3600 seconds (60 minutes)
        self.clock.advance(3600.0)
        res = self.manager.write_frame(frame)
        self.assertEqual(res, "full", "Must force full refresh after 60 minutes")
        self.assertEqual(self.panel.full_refreshes, 2)
        self.assertEqual(self.manager.consecutive_partials, 0)

    def test_offline_dot_stamping_and_guard(self):
        """
        Verifies:
          - stamp_offline_dot modifies rows 0..7 byte 99 to 0x00 (8x8 black square)
          - OfflineGuard triggers dot stamping and one partial refresh when offline > 120s
          - Reconnection forces full refresh and clears offline state
        """
        all_white_png = make_test_png(800, 480, 1, fill_byte=0xFF)
        dotted_png = stamp_offline_dot(all_white_png)

        is_valid, _ = validate_png(dotted_png)
        self.assertTrue(is_valid, "Dotted PNG must remain valid 800x480 1-bit PNG")

        # Verify pixel bytes in decompressed stream
        pos = 8
        idats = []
        while pos < len(dotted_png):
            length, tag = struct.unpack(">I4s", dotted_png[pos : pos + 8])
            if tag == b"IDAT":
                idats.append(dotted_png[pos + 8 : pos + 8 + length])
            pos += 12 + length

        raw = bytearray(zlib.decompress(b"".join(idats)))
        # Rows 0..7, byte 99 (index 100 with filter) must be 0x00 (8 black pixels)
        for y in range(8):
            self.assertEqual(raw[y * 101 + 100], 0x00)
        # Row 8, byte 99 must remain white (0xFF)
        self.assertEqual(raw[8 * 101 + 100], 0xFF)

        # Test OfflineGuard lifecycle
        with tempfile.TemporaryDirectory() as tmpdir:
            persist_file = os.path.join(tmpdir, "last.png")
            guard = OfflineGuard(
                self.config,
                self.manager,
                persistence_path=persist_file,
                time_fn=self.clock.time,
            )

            # Simulate initial boot write to panel (establishes initial full refresh)
            self.manager.write_frame(all_white_png)
            self.assertEqual(self.panel.full_refreshes, 1)

            # Record healthy frame at t=1000
            guard.record_healthy(image_data=all_white_png)
            self.assertEqual(guard.last_good_image, all_white_png)
            self.assertTrue(os.path.isfile(persist_file))

            # At t=1100 (100s disconnected): within grace period (120s), no dot
            self.clock.advance(100.0)
            stamped = guard.check_offline_status(is_connected=False)
            self.assertFalse(stamped)
            self.assertFalse(guard.offline_dot_stamped)

            # At t=1125 (125s disconnected): grace period exceeded, stamps dot!
            self.clock.advance(25.0)
            stamped = guard.check_offline_status(is_connected=False)
            self.assertTrue(stamped)
            self.assertTrue(guard.offline_dot_stamped)
            self.assertTrue(guard.is_offline)
            # Exactly one partial refresh was rendered for the dot
            self.assertEqual(self.panel.partial_refreshes, 1)

            # Reconnection occurs: record_healthy returns was_offline=True
            was_offline = guard.record_healthy()
            self.assertTrue(was_offline, "Must indicate node was offline to schedule full refresh")
            self.assertFalse(guard.is_offline)
            self.assertFalse(guard.offline_dot_stamped)

    def test_shutdown_clear(self):
        """
        Verifies that clear_on_shutdown: true clears panel and sleeps on shutdown.
        """
        self.config.refresh.clear_on_shutdown = True
        self.manager.shutdown()
        self.assertEqual(self.panel.clears, 1)
        self.assertEqual(self.panel.sleeps, 1)

        # When clear_on_shutdown: false, panel is untouched
        panel2 = FakePanel()
        cfg2 = EinkConfig()
        cfg2.refresh.clear_on_shutdown = False
        mgr2 = PanelManager(cfg2, panel=panel2)
        mgr2.shutdown()
        self.assertEqual(panel2.clears, 0)

    def test_hardware_buttons(self):
        """
        Verifies ButtonHandler triggers on_force_refresh and on_advance_screen callbacks.
        """
        self.config.buttons.enabled = True
        self.config.buttons.refresh_pin = 5
        self.config.buttons.next_screen_pin = 6

        refresh_called = []
        advance_called = []

        class MockButton:
            def __init__(self, pin, bounce_time=None):
                self.pin = pin
                self.when_pressed = None

            def press(self):
                if self.when_pressed:
                    self.when_pressed()

        handler = ButtonHandler(
            config=self.config,
            on_force_refresh=lambda: refresh_called.append(True),
            on_advance_screen=lambda: advance_called.append(True),
            gpio_button_factory=MockButton,
        )

        handler.btn_refresh.press()
        self.assertEqual(len(refresh_called), 1)

        handler.btn_advance.press()
        self.assertEqual(len(advance_called), 1)

        handler.close()

    def test_health_server_telemetry(self):
        """
        Verifies GET /healthz endpoint on port 8099 returns valid diagnostic JSON.
        """
        telemetry_data = {
            "status": "ok",
            "connection_state": "online",
            "last_write_time": 1727330000.0,
            "last_etag": '"test-etag-123"',
            "partials_since_full_refresh": 3,
            "total_full_refreshes": 1,
            "total_partial_refreshes": 3,
            "is_offline": False,
            "offline_dot_stamped": False,
            "uptime_seconds": 45.2,
        }

        server = HealthServer(port=8099, telemetry_provider=lambda: telemetry_data)
        server.start()
        time.sleep(0.05)

        try:
            req = urllib.request.Request("http://127.0.0.1:8099/healthz")
            with urllib.request.urlopen(req, timeout=2.0) as resp:
                self.assertEqual(resp.status, 200)
                data = json.loads(resp.read().decode("utf-8"))
                self.assertEqual(data["status"], "ok")
                self.assertEqual(data["connection_state"], "online")
                self.assertEqual(data["last_etag"], '"test-etag-123"')
                self.assertEqual(data["partials_since_full_refresh"], 3)
        finally:
            server.stop()


if __name__ == "__main__":
    unittest.main()
