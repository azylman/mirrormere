import asyncio
import os
import sys
import unittest
from unittest.mock import AsyncMock, MagicMock, patch

# Ensure sidecars/remote is on sys.path even when discover is run from repository root
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from coords import clamp, normalize_coordinates, to_hid_digitizer, to_hid_mouse_delta

from keycodes import (
    lookup_consumer_key,
    lookup_remote_key,
    resolve_key_dispatch,
    CONSUMER_KEYCODES,
    REMOTE_KEYCODES,
)
import daemon

class TestCoords(unittest.TestCase):
    def test_clamp(self):
        self.assertEqual(clamp(0.5, 0.0, 1.0), 0.5)
        self.assertEqual(clamp(-0.5, 0.0, 1.0), 0.0)
        self.assertEqual(clamp(1.5, 0.0, 1.0), 1.0)
        self.assertEqual(clamp(10, 0, 100), 10)
        self.assertEqual(clamp(-5, 0, 100), 0)
        self.assertEqual(clamp(150, 0, 100), 100)

    def test_normalize_coordinates(self):
        # Center of default 2880x1620
        x, y = normalize_coordinates(1440.0, 810.0)
        self.assertAlmostEqual(x, 0.5)
        self.assertAlmostEqual(y, 0.5)

        # Upper-left corner
        x, y = normalize_coordinates(0.0, 0.0)
        self.assertAlmostEqual(x, 0.0)
        self.assertAlmostEqual(y, 0.0)

        # Bottom-right corner
        x, y = normalize_coordinates(2880.0, 1620.0)
        self.assertAlmostEqual(x, 1.0)
        self.assertAlmostEqual(y, 1.0)

        # Out of bounds clamping
        x, y = normalize_coordinates(-100.0, 2000.0)
        self.assertAlmostEqual(x, 0.0)
        self.assertAlmostEqual(y, 1.0)

        # Zero or negative dimension safety
        x, y = normalize_coordinates(100.0, 100.0, width=0, height=0)
        self.assertEqual((x, y), (0.0, 0.0))

    def test_to_hid_digitizer(self):
        x, y = to_hid_digitizer(0.0, 0.0)
        self.assertEqual(x, 0)
        self.assertEqual(y, 0)

        x, y = to_hid_digitizer(1.0, 1.0)
        self.assertEqual(x, 32767)
        self.assertEqual(y, 32767)

        x, y = to_hid_digitizer(0.5, 0.5)
        self.assertEqual(x, 16383)
        self.assertEqual(y, 16383)

        # Clamping
        x, y = to_hid_digitizer(-0.2, 1.5)
        self.assertEqual(x, 0)
        self.assertEqual(y, 32767)

    def test_to_hid_mouse_delta(self):
        dx, dy = to_hid_mouse_delta(10.0, -15.0)
        self.assertEqual(dx, 10)
        self.assertEqual(dy, -15)

        # Extreme values clamped to -127..127
        dx, dy = to_hid_mouse_delta(500.0, -500.0)
        self.assertEqual(dx, 127)
        self.assertEqual(dy, -127)


class TestKeycodes(unittest.TestCase):
    def test_lookup_consumer_key(self):
        self.assertEqual(lookup_consumer_key("back"), 0x0224)
        self.assertEqual(lookup_consumer_key("  BACK  "), 0x0224)
        self.assertEqual(lookup_consumer_key("home"), 0x0223)
        self.assertEqual(lookup_consumer_key("play"), 0x00CD)
        self.assertEqual(lookup_consumer_key("pause"), 0x00CD)
        self.assertEqual(lookup_consumer_key("volup"), 0x00E9)
        self.assertEqual(lookup_consumer_key("voldown"), 0x00EA)
        self.assertIsNone(lookup_consumer_key("unknown_key_xyz"))

    def test_lookup_remote_key(self):
        self.assertEqual(lookup_remote_key("up"), "DPAD_UP")
        self.assertEqual(lookup_remote_key("down"), "DPAD_DOWN")
        self.assertEqual(lookup_remote_key("left"), "DPAD_LEFT")
        self.assertEqual(lookup_remote_key("right"), "DPAD_RIGHT")
        self.assertEqual(lookup_remote_key("select"), "DPAD_CENTER")
        self.assertEqual(lookup_remote_key("enter"), "DPAD_CENTER")
        self.assertEqual(lookup_remote_key("ok"), "DPAD_CENTER")
        self.assertEqual(lookup_remote_key("back"), "BACK")
        self.assertEqual(lookup_remote_key("home"), "HOME")
        self.assertIsNone(lookup_remote_key("nonexistent_command"))

    def test_resolve_key_dispatch(self):
        hid, remote = resolve_key_dispatch("back")
        self.assertEqual(hid, 0x0224)
        self.assertEqual(remote, "BACK")

        hid, remote = resolve_key_dispatch("up")
        self.assertIsNone(hid)
        self.assertEqual(remote, "DPAD_UP")

        hid, remote = resolve_key_dispatch("invalid_test_key")
        self.assertIsNone(hid)
        self.assertIsNone(remote)


class TestDaemonComponents(unittest.TestCase):
    def test_android_remote_helper_missing_credentials(self):
        helper = daemon.AndroidRemoteHelper("127.0.0.1", "/tmp/nonexistent-certs-dir")
        self.assertFalse(helper.has_credentials())
        # Sending keys when disconnected returns False safely
        self.assertFalse(helper.send_key("DPAD_CENTER"))
        self.assertFalse(helper.launch_app("https://youtube.com"))

    def test_touch_reader_resolve_device(self):
        reader = daemon.TouchReader(None, "/tmp/nonexistent-input-event")
        # When device does not exist, resolve_device safely returns None or existing match
        dev = reader.resolve_device()
        self.assertTrue(dev is None or os.path.exists(dev))

    def test_remote_daemon_initialization(self):
        d = daemon.RemoteDaemon()
        self.assertIsNotNone(d.remote_helper)
        self.assertIsNotNone(d.touch_reader)

    def test_advert_name_default(self):
        self.assertEqual(daemon.ADVERT_NAME, "Mirrormere Remote")

if __name__ == "__main__":
    unittest.main()
