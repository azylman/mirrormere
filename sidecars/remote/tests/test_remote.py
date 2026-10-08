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
        self.assertEqual(lookup_remote_key("dpad_center"), "DPAD_CENTER")
        self.assertEqual(lookup_remote_key("dpad_up"), "DPAD_UP")
        self.assertEqual(lookup_remote_key("dpad_down"), "DPAD_DOWN")
        self.assertEqual(lookup_remote_key("dpad_left"), "DPAD_LEFT")
        self.assertEqual(lookup_remote_key("dpad_right"), "DPAD_RIGHT")
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

    def test_auto_approve_pairing_sequence(self):
        helper = daemon.AndroidRemoteHelper("127.0.0.1", "/tmp/nonexistent-certs-dir")
        helper.connected = True
        helper.remote = MagicMock()
        asyncio.run(helper.auto_approve_pairing())
        self.assertEqual(helper.remote.send_key_command.call_count, 0)

    def test_configure_adapter(self):
        d = daemon.RemoteDaemon()
        d.adapter = AsyncMock()
        with patch("asyncio.create_subprocess_exec", new_callable=AsyncMock) as mock_exec:
            proc = AsyncMock()
            proc.wait = AsyncMock(return_value=0)
            mock_exec.return_value = proc
            asyncio.run(d._configure_adapter())
            d.adapter.set_powered.assert_awaited_once_with(True)
            d.adapter.set_alias.assert_awaited_once_with(d.config.advert_name)
            self.assertEqual(mock_exec.call_count, 3)

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

    def test_advertisement_fallback(self):
        self.assertIsNone(daemon.Advertisement)
        self.assertIsNone(daemon.KioskAdvertisement)

    def test_remote_daemon_custom_config(self):
        from config import RemoteConfig
        cfg = RemoteConfig(
            chromecast_host="10.0.0.50",
            advert_name="Test Remote",
            http_port=8095,
        )
        d = daemon.RemoteDaemon(config=cfg)
        self.assertEqual(d.config.chromecast_host, "10.0.0.50")
        self.assertEqual(d.config.advert_name, "Test Remote")
        self.assertEqual(d.config.http_port, 8095)

    def test_handle_bluetooth_remove(self):
        d = daemon.RemoteDaemon()
        req = MagicMock()
        async def mock_json():
            return {"mac": "DC:E5:5B:A6:30:8B"}
        req.json = mock_json
        with patch.object(daemon, "web") as mock_web, patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            mock_proc = MagicMock()
            async def mock_wait():
                return 0
            mock_proc.wait = mock_wait
            mock_exec.return_value = mock_proc
            resp = asyncio.run(d.handle_bluetooth_remove(req))
            self.assertEqual(resp.status, 200)

    def test_handle_bluetooth_remove_missing_mac(self):
        d = daemon.RemoteDaemon()
        req = MagicMock()
        async def mock_json():
            return {}
        req.json = mock_json
        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(d.handle_bluetooth_remove(req))
            self.assertEqual(resp.status, 400)

    def test_pairing_agent_capability_display_yes_no(self):
        if daemon.PairingAgent is not None and daemon.AgentCapability is not None:
            self.assertEqual(daemon.PairingAgent.capability, daemon.AgentCapability.DISPLAY_YES_NO)


class TestConfig(unittest.TestCase):
    def test_default_config(self):
        from config import load_config
        cfg = load_config("/nonexistent/path/to/config.yaml")
        self.assertEqual(cfg.chromecast_host, "chromecast.lan")
        self.assertEqual(cfg.cert_dir, "/data/certs")
        self.assertEqual(cfg.touch_device, "/dev/input/event3")
        self.assertEqual(cfg.http_port, 8092)
        self.assertEqual(cfg.advert_name, "Mirrormere Remote")
        self.assertTrue(cfg.auto_confirm_pairing)

    def test_load_from_yaml(self):
        from config import load_config
        import tempfile
        content = """
chromecast_host: 10.0.0.50
cert_dir: /custom/certs
touch_device: /dev/input/event5
http_port: 8099
advert_name: Custom Remote
auto_confirm_pairing: false
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.chromecast_host, "10.0.0.50")
            self.assertEqual(cfg.cert_dir, "/custom/certs")
            self.assertEqual(cfg.touch_device, "/dev/input/event5")
            self.assertEqual(cfg.http_port, 8099)
            self.assertEqual(cfg.advert_name, "Custom Remote")
            self.assertFalse(cfg.auto_confirm_pairing)
        finally:
            os.remove(temp_path)

    def test_load_from_scoped_yaml(self):
        from config import load_config
        import tempfile
        content = """
remote:
  chromecast_addr: 10.0.0.50:8009
  cert_dir: /kiosk/certs
  touch_device: /dev/input/event3
  http_port: 8092
  advert_name: Mirrormere Remote
  auto_confirm_pairing: true
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.chromecast_host, "10.0.0.50")
            self.assertEqual(cfg.cert_dir, "/kiosk/certs")
            self.assertTrue(cfg.auto_confirm_pairing)
        finally:
            os.remove(temp_path)

    def test_no_env_fallback(self):
        # In accordance with system invariants, environment variables must not override config values
        from config import load_config
        with patch.dict(os.environ, {
            "CHROMECAST_HOST": "malicious-host.lan",
            "ADVERT_NAME": "Ignored Advert",
            "HTTP_PORT": "9999",
        }):
            cfg = load_config("/nonexistent/file.yaml")
            self.assertEqual(cfg.chromecast_host, "chromecast.lan")
            self.assertEqual(cfg.advert_name, "Mirrormere Remote")
            self.assertEqual(cfg.http_port, 8092)


if __name__ == "__main__":
    unittest.main()
