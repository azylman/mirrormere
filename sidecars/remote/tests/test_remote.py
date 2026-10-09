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

        # Dynamic absinfo range (e.g. 1080p panel: 0..1920, 0..1080)
        x, y = normalize_coordinates(960.0, 540.0, min_x=0.0, max_x=1920.0, min_y=0.0, max_y=1080.0)
        self.assertAlmostEqual(x, 0.5)
        self.assertAlmostEqual(y, 0.5)

        # Offset range (e.g. digitizer with non-zero min: 100..1100)
        x, y = normalize_coordinates(600.0, 600.0, min_x=100.0, max_x=1100.0, min_y=100.0, max_y=1100.0)
        self.assertAlmostEqual(x, 0.5)
        self.assertAlmostEqual(y, 0.5)

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

    def test_touch_reader_update_absinfo(self):
        reader = daemon.TouchReader(None, "/tmp/test-event")
        mock_dev = MagicMock()
        mock_x = MagicMock()
        mock_x.min = 0
        mock_x.max = 1920
        mock_y = MagicMock()
        mock_y.min = 0
        mock_y.max = 1080

        def mock_absinfo(code):
            # Assuming EV_ABS codes: X is 0x35 (ABS_MT_POSITION_X) or 0x00 (ABS_X), Y is 0x36 or 0x01
            if code in (0x35, 0x00):
                return mock_x
            if code in (0x36, 0x01):
                return mock_y
            return None

        mock_dev.absinfo.side_effect = mock_absinfo
        reader.update_absinfo(mock_dev)
        self.assertEqual(reader.min_x, 0.0)
        self.assertEqual(reader.max_x, 1920.0)
        self.assertEqual(reader.min_y, 0.0)
        self.assertEqual(reader.max_y, 1080.0)

        reader.cur_x = 960
        reader.cur_y = 540
        x_r, y_r = reader._normalize()
        self.assertAlmostEqual(x_r, 0.5)
        self.assertAlmostEqual(y_r, 0.5)

    def test_remote_daemon_initialization(self):
        d = daemon.RemoteDaemon()
        self.assertIsNotNone(d.remote_helper)
        self.assertIsNotNone(d.touch_reader)

    def test_advert_name_default(self):
        self.assertEqual(daemon.ADVERT_NAME, "Mirrormere Remote")

    def test_advertisement_fallback(self):
        self.assertIsNone(daemon.Advertisement)
        self.assertIsNone(daemon.KioskAdvertisement)
        self.assertIsNone(daemon.FastKioskAdvertisement)

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
            mock_proc.communicate = AsyncMock(return_value=(b"", b""))
            mock_proc.wait = AsyncMock(return_value=0)
            mock_proc.returncode = 0
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

    def test_pairing_agent_capability_no_input_no_output(self):
        if daemon.PairingAgent is not None and daemon.AgentCapability is not None:
            self.assertEqual(daemon.PairingAgent.capability, daemon.AgentCapability.NO_INPUT_NO_OUTPUT)


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


class TestHIDDescriptor(unittest.TestCase):
    def test_report_map_multi_report(self):
        # REPORT_MAP must contain generic mouse usage (0x05, 0x01, 0x09, 0x02)
        mouse_usage = bytes([0x05, 0x01, 0x09, 0x02])
        self.assertIn(mouse_usage, daemon.REPORT_MAP)

        # REPORT_MAP must contain Touch Screen digitizer usage (0x05, 0x0d, 0x09, 0x04)
        touch_usage = bytes([0x05, 0x0d, 0x09, 0x04])
        self.assertIn(touch_usage, daemon.REPORT_MAP)

        # REPORT_MAP must contain Consumer Control usage (0x05, 0x0c, 0x09, 0x01)
        consumer_usage = bytes([0x05, 0x0c, 0x09, 0x01])
        self.assertIn(consumer_usage, daemon.REPORT_MAP)

    def test_multi_report_ids(self):
        # Report 1 must be Relative Mouse (Report ID 1)
        self.assertIn(bytes([0x85, 0x01]), daemon.REPORT_MAP)
        # Report 2 must be Digitizer Touch Screen (Report ID 2)
        self.assertIn(bytes([0x85, 0x02]), daemon.REPORT_MAP)
        # Report 3 must be Consumer Control (Report ID 3)
        self.assertIn(bytes([0x85, 0x03]), daemon.REPORT_MAP)


class TestTouchEndpoint(unittest.TestCase):
    def setUp(self):
        self.daemon = daemon.RemoteDaemon()
        self.daemon.hid = MagicMock()

    def test_handle_touch_tap(self):
        req = MagicMock()
        async def mock_json():
            return {"action": "tap", "x": 0.25, "y": 0.75}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_touch(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["action"], "tap")
            self.assertEqual(resp.data["x"], 0.25)
            self.assertEqual(resp.data["y"], 0.75)
            self.assertEqual(self.daemon.hid.send_mouse.call_count, 2)
            self.daemon.hid.send_mouse.assert_any_call(1, 0, 0)
            self.daemon.hid.send_mouse.assert_any_call(0, 0, 0)
            self.assertEqual(self.daemon.hid.send_touch.call_count, 2)
            self.daemon.hid.send_touch.assert_any_call(True, 0.25, 0.75)
            self.daemon.hid.send_touch.assert_any_call(False, 0.25, 0.75)

    def test_handle_touch_down_move_up(self):
        for action, tip_down, expect_mouse, mouse_args in [
            ("down", True, True, (1, 0, 0)),
            ("move", True, False, None),
            ("up", False, True, (0, 0, 0)),
        ]:
            self.daemon.hid.reset_mock()
            req = MagicMock()
            async def make_json(act=action):
                return {"action": act, "x": 0.4, "y": 0.6}
            req.json = make_json

            with patch.object(daemon, "web") as mock_web:
                mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
                resp = asyncio.run(self.daemon.handle_touch(req))
                self.assertEqual(resp.status, 200)
                self.assertEqual(resp.data["action"], action)
                self.daemon.hid.send_touch.assert_called_once_with(tip_down, 0.4, 0.6)
                if expect_mouse:
                    self.daemon.hid.send_mouse.assert_called_once_with(*mouse_args)
                else:
                    self.daemon.hid.send_mouse.assert_not_called()

    def test_handle_tap_endpoint(self):
        req = MagicMock()
        async def mock_json():
            return {"x": 0.3, "y": 0.7}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_tap(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["action"], "tap")
            self.assertEqual(resp.data["x"], 0.3)
            self.assertEqual(resp.data["y"], 0.7)
            self.assertEqual(self.daemon.hid.send_mouse.call_count, 2)
            self.daemon.hid.send_mouse.assert_any_call(1, 0, 0)
            self.daemon.hid.send_mouse.assert_any_call(0, 0, 0)
            self.assertEqual(self.daemon.hid.send_touch.call_count, 2)
            self.daemon.hid.send_touch.assert_any_call(True, 0.3, 0.7)
            self.daemon.hid.send_touch.assert_any_call(False, 0.3, 0.7)

    def test_handle_tap_finally_releases_on_error(self):
        req = MagicMock()
        async def mock_json():
            return {"x": 0.5, "y": 0.5}
        req.json = mock_json

        # Simulate sleep raising an exception mid-tap
        with patch("asyncio.sleep", side_effect=RuntimeError("simulated mid-tap error")):
            with self.assertRaises(RuntimeError):
                asyncio.run(self.daemon.handle_tap(req))

        # Mouse and touch release should still have been called in finally
        self.daemon.hid.send_mouse.assert_any_call(0, 0, 0)
        self.daemon.hid.send_touch.assert_any_call(False, 0.5, 0.5)

    def test_handle_touch_clamping(self):
        req = MagicMock()
        async def mock_json():
            return {"action": "tap", "x": -0.5, "y": 1.5}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_touch(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["x"], 0.0)
            self.assertEqual(resp.data["y"], 1.0)

    def test_handle_touch_unknown_action(self):
        req = MagicMock()
        async def mock_json():
            return {"action": "wiggle", "x": 0.5, "y": 0.5}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_touch(req))
            self.assertEqual(resp.status, 400)

    def test_handle_touch_no_hid(self):
        self.daemon.hid = None
        req = MagicMock()
        async def mock_json():
            return {"action": "tap", "x": 0.5, "y": 0.5}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_touch(req))
            self.assertEqual(resp.status, 503)


class TestBluetoothKeepalive(unittest.TestCase):
    def setUp(self):
        self.daemon = daemon.RemoteDaemon()

    def test_reconnect_disconnected_paired_device(self):
        paired_stdout = b"Device DC:E5:5B:A6:30:8B Chromecast\n"
        info_stdout = b"Device DC:E5:5B:A6:30:8B (public)\n\tName: Chromecast\n\tConnected: no\n\tPaired: yes\n"

        async def mock_create_subprocess_exec(*cmd, **kwargs):
            proc = MagicMock()
            if cmd == ("bluetoothctl", "devices", "Paired"):
                async def mock_communicate():
                    return paired_stdout, b""
                proc.communicate = mock_communicate
                proc.returncode = 0
            elif len(cmd) == 3 and cmd[:2] == ("bluetoothctl", "info"):
                async def mock_communicate():
                    return info_stdout, b""
                proc.communicate = mock_communicate
                proc.returncode = 0
            elif len(cmd) == 3 and cmd[:2] == ("bluetoothctl", "connect"):
                async def mock_communicate():
                    return b"Connection successful\n", b""
                proc.communicate = mock_communicate
                proc.returncode = 0
            return proc

        with patch("asyncio.create_subprocess_exec", side_effect=mock_create_subprocess_exec):
            results = asyncio.run(self.daemon._check_and_reconnect_bluetooth())
            self.assertEqual(len(results), 1)
            self.assertEqual(results[0]["mac"], "DC:E5:5B:A6:30:8B")
            self.assertTrue(results[0]["reconnected"])

    def test_already_connected_device_no_reconnect_attempt(self):
        paired_stdout = b"Device DC:E5:5B:A6:30:8B Chromecast\n"
        info_stdout = b"Device DC:E5:5B:A6:30:8B (public)\n\tName: Chromecast\n\tConnected: yes\n\tPaired: yes\n"

        connect_called = False
        async def mock_create_subprocess_exec(*cmd, **kwargs):
            nonlocal connect_called
            proc = MagicMock()
            if cmd == ("bluetoothctl", "devices", "Paired"):
                async def mock_communicate():
                    return paired_stdout, b""
                proc.communicate = mock_communicate
                proc.returncode = 0
            elif len(cmd) == 3 and cmd[:2] == ("bluetoothctl", "info"):
                async def mock_communicate():
                    return info_stdout, b""
                proc.communicate = mock_communicate
                proc.returncode = 0
            elif len(cmd) == 3 and cmd[:2] == ("bluetoothctl", "connect"):
                connect_called = True
            return proc

        with patch("asyncio.create_subprocess_exec", side_effect=mock_create_subprocess_exec):
            results = asyncio.run(self.daemon._check_and_reconnect_bluetooth())
            self.assertEqual(len(results), 0)
            self.assertFalse(connect_called)

    def test_handle_bluetooth_reconnect_endpoint(self):
        req = MagicMock()
        async def mock_json():
            return {"mac": "DC:E5:5B:A6:30:8B"}
        req.json = mock_json

        with patch.object(self.daemon, "_check_and_reconnect_bluetooth", new_callable=AsyncMock) as mock_recon, \
             patch.object(daemon, "web") as mock_web:
            mock_recon.return_value = [{"mac": "DC:E5:5B:A6:30:8B", "reconnected": True}]
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_bluetooth_reconnect(req))
            self.assertEqual(resp.status, 200)
            self.assertTrue(resp.data["reconnected"][0]["reconnected"])

    def test_run_bluetoothctl_timeout_kills_and_reaps(self):
        mock_proc = MagicMock()
        mock_proc.kill = MagicMock()
        mock_proc.wait = AsyncMock(return_value=0)

        async def hanging_communicate():
            await asyncio.sleep(10.0)
            return b"", b""

        mock_proc.communicate = hanging_communicate

        with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
            with self.assertRaises(asyncio.TimeoutError):
                asyncio.run(self.daemon._run_bluetoothctl("test", timeout=0.05))

            mock_proc.kill.assert_called_once()
            mock_proc.wait.assert_awaited_once()

    def test_cleanup_stale_connections(self):
        cmd_log = []
        async def mock_exec(*cmd, **kwargs):
            cmd_log.append(cmd)
            proc = MagicMock(returncode=0)
            if cmd == ("bluetoothctl", "devices", "Paired"):
                proc.communicate = AsyncMock(return_value=(b"Device DC:E5:5B:A6:30:8B TV\nDevice 11:22:33:44:55:66 Other\n", b""))
            elif cmd[:2] == ("bluetoothctl", "info"):
                proc.communicate = AsyncMock(return_value=(b"Connected: yes\n" if "DC:E5" in cmd[2] else b"Connected: no\n", b""))
            else:
                proc.communicate = AsyncMock(return_value=(b"", b""))
            return proc

        with patch("asyncio.create_subprocess_exec", side_effect=mock_exec):
            asyncio.run(self.daemon._cleanup_stale_connections())
            self.assertIn(("bluetoothctl", "disconnect", "DC:E5:5B:A6:30:8B"), cmd_log)
            self.assertNotIn(("bluetoothctl", "disconnect", "11:22:33:44:55:66"), cmd_log)

    def test_reconnect_force_flag_disconnects_first(self):
        cmd_log = []
        async def mock_exec(*cmd, **kwargs):
            cmd_log.append(cmd)
            proc = MagicMock(returncode=0)
            if cmd == ("bluetoothctl", "devices", "Paired"):
                proc.communicate = AsyncMock(return_value=(b"Device DC:E5:5B:A6:30:8B TV\n", b""))
            elif cmd[:2] == ("bluetoothctl", "info"):
                proc.communicate = AsyncMock(return_value=(b"Connected: yes\nServicesResolved: yes\n", b""))
            else:
                proc.communicate = AsyncMock(return_value=(b"", b""))
            return proc

        with patch("asyncio.create_subprocess_exec", side_effect=mock_exec):
            results = asyncio.run(self.daemon._check_and_reconnect_bluetooth(force=True))
            self.assertTrue(results[0]["reconnected"])
            disconnect_idx = cmd_log.index(("bluetoothctl", "disconnect", "DC:E5:5B:A6:30:8B"))
            connect_idx = cmd_log.index(("bluetoothctl", "connect", "DC:E5:5B:A6:30:8B"))
            self.assertLess(disconnect_idx, connect_idx)

    def test_handle_bluetooth_reconnect_passes_force(self):
        req = MagicMock()
        async def mock_json():
            return {"mac": "DC:E5:5B:A6:30:8B", "force": True}
        req.json = mock_json

        with patch.object(self.daemon, "_check_and_reconnect_bluetooth", new_callable=AsyncMock) as mock_recon, \
             patch.object(daemon, "web") as mock_web:
            mock_recon.return_value = [{"mac": "DC:E5:5B:A6:30:8B", "reconnected": True}]
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_bluetooth_reconnect(req))
            self.assertEqual(resp.status, 200)
            mock_recon.assert_awaited_once_with(target_mac="DC:E5:5B:A6:30:8B", force=True)


class TestConfigKeepalive(unittest.TestCase):
    def test_defaults(self):
        from config import RemoteConfig
        cfg = RemoteConfig()
        self.assertFalse(cfg.enable_evdev)
        self.assertTrue(cfg.bluetooth_keepalive)
        self.assertEqual(cfg.bluetooth_keepalive_interval, 10.0)
        self.assertEqual(cfg.advert_min_interval, 30)
        self.assertEqual(cfg.advert_max_interval, 50)

    def test_advert_interval_defaults_and_overrides(self):
        from config import RemoteConfig, load_config
        import tempfile
        content = """
advert_min_interval: 25
advert_max_interval: 45
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.advert_min_interval, 25)
            self.assertEqual(cfg.advert_max_interval, 45)
        finally:
            os.remove(temp_path)

    def test_load_keepalive_overrides(self):
        from config import load_config
        import tempfile
        content = """
enable_evdev: true
bluetooth_keepalive: false
bluetooth_keepalive_interval: 20.0
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertTrue(cfg.enable_evdev)
            self.assertFalse(cfg.bluetooth_keepalive)
            self.assertEqual(cfg.bluetooth_keepalive_interval, 20.0)
        finally:
            os.remove(temp_path)


class TestCorsMiddleware(unittest.TestCase):
    def test_cors_options(self):
        if not daemon.cors_middleware:
            self.skipTest("aiohttp not installed")
        req = MagicMock()
        req.method = "OPTIONS"
        handler = AsyncMock()

        resp = asyncio.run(daemon.cors_middleware(req, handler))
        self.assertEqual(resp.status, 204)
        self.assertEqual(resp.headers["Access-Control-Allow-Origin"], "*")
        self.assertIn("OPTIONS", resp.headers["Access-Control-Allow-Methods"])

    def test_cors_get_or_post(self):
        if not daemon.cors_middleware:
            self.skipTest("aiohttp not installed")
        req = MagicMock()
        req.method = "POST"
        mock_resp = MagicMock()
        mock_resp.headers = {}
        handler = AsyncMock(return_value=mock_resp)

        resp = asyncio.run(daemon.cors_middleware(req, handler))
        self.assertEqual(resp.headers["Access-Control-Allow-Origin"], "*")
        self.assertIn("POST", resp.headers["Access-Control-Allow-Methods"])


if __name__ == "__main__":
    unittest.main()
