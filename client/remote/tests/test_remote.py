import asyncio
import os
import sys
import unittest
from unittest.mock import AsyncMock, MagicMock, patch

# Ensure client/remote is on sys.path even when discover is run from repository root
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

        # 16:9 Widescreen aspect ratio (32767 x 18431)
        x, y = to_hid_digitizer(1.0, 1.0)
        self.assertEqual(x, 32767)
        self.assertEqual(y, 18431)

        x, y = to_hid_digitizer(0.5, 0.5)
        self.assertEqual(x, 16383)
        self.assertEqual(y, 9215)

        # Clamping
        x, y = to_hid_digitizer(-0.2, 1.5)
        self.assertEqual(x, 0)
        self.assertEqual(y, 18431)

        # Legacy explicit max_logical override
        x, y = to_hid_digitizer(1.0, 1.0, max_logical=32767)
        self.assertEqual(x, 32767)
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
        self.assertEqual(lookup_consumer_key("rewind"), 0x00B4)
        self.assertEqual(lookup_consumer_key("fast_forward"), 0x00B3)
        self.assertEqual(lookup_consumer_key("fastforward"), 0x00B3)
        self.assertEqual(lookup_consumer_key("skip_backward"), 0x00B6)
        self.assertEqual(lookup_consumer_key("skip_forward"), 0x00B5)
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
        self.assertEqual(lookup_remote_key("rewind"), "MEDIA_REWIND")
        self.assertEqual(lookup_remote_key("fast_forward"), "MEDIA_FAST_FORWARD")
        self.assertEqual(lookup_remote_key("fastforward"), "MEDIA_FAST_FORWARD")
        self.assertEqual(lookup_remote_key("media_rewind"), "MEDIA_REWIND")
        self.assertEqual(lookup_remote_key("media_fast_forward"), "MEDIA_FAST_FORWARD")
        self.assertIsNone(lookup_remote_key("nonexistent_command"))

    def test_resolve_key_dispatch(self):
        hid, remote = resolve_key_dispatch("back")
        self.assertEqual(hid, 0x0224)
        self.assertEqual(remote, "BACK")

        hid, remote = resolve_key_dispatch("home")
        self.assertEqual(hid, 0x0223)
        self.assertEqual(remote, "HOME")

        hid, remote = resolve_key_dispatch("cc")
        self.assertEqual(hid, 0x0061)
        self.assertEqual(remote, "CAPTIONS")

        hid, remote = resolve_key_dispatch("next")
        self.assertEqual(hid, 0x00B5)
        self.assertEqual(remote, "MEDIA_NEXT")

        hid, remote = resolve_key_dispatch("prev")
        self.assertEqual(hid, 0x00B6)
        self.assertEqual(remote, "MEDIA_PREVIOUS")

        hid, remote = resolve_key_dispatch("rewind")
        self.assertEqual(hid, 0x00B4)
        self.assertEqual(remote, "MEDIA_REWIND")

        hid, remote = resolve_key_dispatch("fast_forward")
        self.assertEqual(hid, 0x00B3)
        self.assertEqual(remote, "MEDIA_FAST_FORWARD")

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

    def test_android_remote_helper_start_initializes_with_voice(self):
        helper = daemon.AndroidRemoteHelper("127.0.0.1", "/tmp/fake-certs")
        mock_module = MagicMock()
        mock_remote_cls = MagicMock()
        mock_module.AndroidTVRemote = mock_remote_cls
        mock_instance = MagicMock()
        mock_instance.async_connect = AsyncMock(side_effect=RuntimeError("test stop"))
        mock_remote_cls.return_value = mock_instance

        with patch.object(helper, "has_credentials", return_value=True), \
             patch.dict(sys.modules, {"androidtvremote2": mock_module, "androidtvremote2.androidtv_remote": mock_module}), \
             patch("asyncio.sleep", side_effect=asyncio.CancelledError):
            try:
                asyncio.run(helper.start())
            except asyncio.CancelledError:
                pass

        mock_remote_cls.assert_called_once_with(
            client_name="Mirrormere Remote",
            certfile=helper.cert_file,
            keyfile=helper.key_file,
            host=helper.host,
            enable_voice=True,
        )

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

    def test_touch_reader_run_pointer_mode_touch(self):
        hid_mock = MagicMock()
        reader = daemon.TouchReader(hid_mock, "/dev/input/event0", pointer_mode="touch")
        reader.cur_x = 1440
        reader.cur_y = 810
        mock_dev = MagicMock()

        class MockEv:
            def __init__(self, t, c, v):
                self.type = t
                self.code = c
                self.value = v

        mock_evdev = MagicMock()
        mock_evdev.ecodes.EV_ABS = 3
        mock_evdev.ecodes.EV_KEY = 1
        mock_evdev.ecodes.EV_SYN = 0
        mock_evdev.ecodes.BTN_TOUCH = 330
        mock_evdev.ecodes.SYN_REPORT = 0
        mock_evdev.ecodes.ABS_MT_TRACKING_ID = 57
        mock_evdev.ecodes.ABS_MT_POSITION_X = 53
        mock_evdev.ecodes.ABS_MT_POSITION_Y = 54
        mock_evdev.ecodes.ABS_X = 0
        mock_evdev.ecodes.ABS_Y = 1

        async def fake_loop():
            # touch down
            yield MockEv(1, 330, 1)
            yield MockEv(0, 0, 0)
            # touch up
            yield MockEv(1, 330, 0)
            raise StopAsyncIteration

        mock_dev.async_read_loop = fake_loop
        mock_evdev.InputDevice.return_value = mock_dev

        async def run():
            with patch.dict("sys.modules", {"evdev": mock_evdev}), \
                 patch.object(reader, "resolve_device", return_value="/dev/input/event0"), \
                 patch("os.path.exists", return_value=True), \
                 patch("asyncio.sleep", side_effect=asyncio.CancelledError):
                try:
                    await reader.run()
                except asyncio.CancelledError:
                    pass

        asyncio.run(run())
        hid_mock.send_mouse.assert_not_called()
        self.assertTrue(hid_mock.send_touch.called)

    def test_touch_reader_run_pointer_mode_mouse(self):
        hid_mock = MagicMock()
        reader = daemon.TouchReader(hid_mock, "/dev/input/event0", pointer_mode="mouse")
        reader.cur_x = 1440
        reader.cur_y = 810
        mock_dev = MagicMock()

        class MockEv:
            def __init__(self, t, c, v):
                self.type = t
                self.code = c
                self.value = v

        mock_evdev = MagicMock()
        mock_evdev.ecodes.EV_ABS = 3
        mock_evdev.ecodes.EV_KEY = 1
        mock_evdev.ecodes.EV_SYN = 0
        mock_evdev.ecodes.BTN_TOUCH = 330
        mock_evdev.ecodes.SYN_REPORT = 0
        mock_evdev.ecodes.ABS_MT_TRACKING_ID = 57
        mock_evdev.ecodes.ABS_MT_POSITION_X = 53
        mock_evdev.ecodes.ABS_MT_POSITION_Y = 54
        mock_evdev.ecodes.ABS_X = 0
        mock_evdev.ecodes.ABS_Y = 1

        async def fake_loop():
            # touch down
            yield MockEv(1, 330, 1)
            yield MockEv(0, 0, 0)
            # touch up
            yield MockEv(1, 330, 0)
            raise StopAsyncIteration

        mock_dev.async_read_loop = fake_loop
        mock_evdev.InputDevice.return_value = mock_dev

        async def run():
            with patch.dict("sys.modules", {"evdev": mock_evdev}), \
                 patch.object(reader, "resolve_device", return_value="/dev/input/event0"), \
                 patch("os.path.exists", return_value=True), \
                 patch("asyncio.sleep", side_effect=asyncio.CancelledError):
                try:
                    await reader.run()
                except asyncio.CancelledError:
                    pass

        asyncio.run(run())
        hid_mock.send_touch.assert_not_called()
        self.assertTrue(hid_mock.send_mouse.called)

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

    def test_pairing_agent_capability_display_yes_no(self):
        if daemon.PairingAgent is not None and daemon.AgentCapability is not None:
            self.assertEqual(daemon.PairingAgent.capability, daemon.AgentCapability.DISPLAY_YES_NO)

    def test_device_info_service_model_number(self):
        dev_info = daemon.DeviceInfoService()
        self.assertEqual(dev_info.model_number({}), b"Mirrormere-Remote-v1")
        self.assertEqual(dev_info.manufacturer_name({}), b"Mirrormere")

    def test_pairing_agent_auto_confirm_true(self):
        async def run_test():
            helper = MagicMock()
            agent = daemon.PairingAgent(helper, auto_confirm=True)
            with patch.object(agent, "_trust_device", new_callable=AsyncMock) as mock_trust:
                agent.RequestConfirmation("/org/bluez/dev_DC_E5_5B_A6_30_8B", 123456)
                agent.RequestAuthorization("/org/bluez/dev_DC_E5_5B_A6_30_8B")
                agent.AuthorizeService("/org/bluez/dev_DC_E5_5B_A6_30_8B", "1812")
                await asyncio.sleep(0.01)
                self.assertEqual(mock_trust.call_count, 3)
                mock_trust.assert_any_call("DC:E5:5B:A6:30:8B")
        asyncio.run(run_test())

    def test_pairing_agent_auto_confirm_false(self):
        helper = MagicMock()
        agent = daemon.PairingAgent(helper, auto_confirm=False)
        with patch.object(agent, "_trust_device", new_callable=AsyncMock) as mock_trust:
            with self.assertRaises(daemon.DBusError):
                agent.RequestConfirmation("/org/bluez/dev_DC_E5_5B_A6_30_8B", 123456)
            with self.assertRaises(daemon.DBusError):
                agent.RequestAuthorization("/org/bluez/dev_DC_E5_5B_A6_30_8B")
            with self.assertRaises(daemon.DBusError):
                agent.AuthorizeService("/org/bluez/dev_DC_E5_5B_A6_30_8B", "1812")
            mock_trust.assert_not_called()

    def test_configure_adapter_states(self):
        d = daemon.RemoteDaemon()
        d.adapter = AsyncMock()
        # auto_confirm_pairing is True -> discoverable on, pairable on
        d.config.auto_confirm_pairing = True
        with patch("asyncio.create_subprocess_exec", new_callable=AsyncMock) as mock_exec:
            proc = AsyncMock()
            proc.wait = AsyncMock(return_value=0)
            mock_exec.return_value = proc
            asyncio.run(d._configure_adapter())
            mock_exec.assert_any_call("bluetoothctl", "discoverable", "on")
            mock_exec.assert_any_call("bluetoothctl", "pairable", "on")

        # auto_confirm_pairing is False -> discoverable off, pairable off
        d.config.auto_confirm_pairing = False
        with patch("asyncio.create_subprocess_exec", new_callable=AsyncMock) as mock_exec:
            proc = AsyncMock()
            proc.wait = AsyncMock(return_value=0)
            mock_exec.return_value = proc
            asyncio.run(d._configure_adapter())
            mock_exec.assert_any_call("bluetoothctl", "discoverable", "off")
            mock_exec.assert_any_call("bluetoothctl", "pairable", "off")

    def test_managed_devices_tracking_and_persistence(self):
        import tempfile, shutil
        temp_dir = tempfile.mkdtemp()
        try:
            from config import RemoteConfig
            cfg = RemoteConfig(cert_dir=temp_dir, managed_devices=["11:22:33:44:55:66"])
            d = daemon.RemoteDaemon(config=cfg)
            self.assertIn("11:22:33:44:55:66", d.managed_macs)

            # Add another managed device
            d.add_managed_device("aa:bb:cc:dd:ee:ff")
            self.assertIn("AA:BB:CC:DD:EE:FF", d.managed_macs)

            # Verify persisted file
            dev_file = os.path.join(temp_dir, "managed_devices.txt")
            self.assertTrue(os.path.exists(dev_file))
            with open(dev_file, "r") as f:
                lines = [l.strip() for l in f]
            self.assertIn("AA:BB:CC:DD:EE:FF", lines)

            # Test reload into new daemon instance
            cfg2 = RemoteConfig(cert_dir=temp_dir)
            d2 = daemon.RemoteDaemon(config=cfg2)
            self.assertIn("AA:BB:CC:DD:EE:FF", d2.managed_macs)

            # Test remove managed device
            d2.remove_managed_device("AA:BB:CC:DD:EE:FF")
            self.assertNotIn("AA:BB:CC:DD:EE:FF", d2.managed_macs)
            with open(dev_file, "r") as f:
                lines_after = [l.strip() for l in f]
            self.assertNotIn("AA:BB:CC:DD:EE:FF", lines_after)
        finally:
            shutil.rmtree(temp_dir)

    def test_cleanup_stale_connections_filtering(self):
        d = daemon.RemoteDaemon()
        d._run_bluetoothctl = AsyncMock()

        # Case 1: managed_macs is populated -> only disconnect devices in managed_macs
        d.managed_macs = {"DC:E5:5B:A6:30:8B"}
        paired_stdout = b"Device DC:E5:5B:A6:30:8B Chromecast\nDevice 00:11:22:33:44:55 Unrelated Headset\n"
        d._run_bluetoothctl.side_effect = [
            (0, paired_stdout, b""),  # devices Paired
            (0, b"Connected: yes\nIcon: video-display\n", b""),  # info for DC:E5:...
            (0, b"", b""),  # disconnect DC:E5:...
        ]
        asyncio.run(d._cleanup_stale_connections())
        d._run_bluetoothctl.assert_any_call("disconnect", "DC:E5:5B:A6:30:8B", timeout=5.0)
        for call in d._run_bluetoothctl.call_args_list:
            self.assertNotIn("00:11:22:33:44:55", call[0])

        # Case 2: managed_macs is empty -> skips standard peripherals (audio, keyboard, etc.)
        d.managed_macs = set()
        d._run_bluetoothctl.reset_mock()
        paired_stdout2 = b"Device AA:BB:CC:00:00:01 Headphones\nDevice AA:BB:CC:00:00:02 Display\n"
        d._run_bluetoothctl.side_effect = [
            (0, paired_stdout2, b""),  # devices Paired
            (0, b"Connected: yes\nIcon: audio-headphones\n", b""),  # info for Headphones (peripheral)
            (0, b"Connected: yes\nIcon: display\n", b""),  # info for Display
            (0, b"", b""),  # disconnect Display
        ]
        asyncio.run(d._cleanup_stale_connections())
        d._run_bluetoothctl.assert_any_call("disconnect", "AA:BB:CC:00:00:02", timeout=5.0)
        for call in d._run_bluetoothctl.call_args_list:
            if call[0][0] == "disconnect":
                self.assertNotEqual(call[0][1], "AA:BB:CC:00:00:01")

    def test_check_and_reconnect_bluetooth_filtering(self):
        d = daemon.RemoteDaemon()
        d._run_bluetoothctl = AsyncMock()

        # Case 1: managed_macs populated -> skips unmanaged
        d.managed_macs = {"DC:E5:5B:A6:30:8B"}
        paired_stdout = b"Device DC:E5:5B:A6:30:8B Chromecast\nDevice 11:22:33:44:55:66 Keyboard\n"
        d._run_bluetoothctl.side_effect = [
            (0, paired_stdout, b""),
            (0, b"Connected: no\n", b""),
            (0, b"", b""),  # connect
        ]
        results = asyncio.run(d._check_and_reconnect_bluetooth())
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["mac"], "DC:E5:5B:A6:30:8B")

        # Case 2: managed_macs empty -> skips standard peripheral icons
        d.managed_macs = set()
        d._run_bluetoothctl.reset_mock()
        paired_stdout2 = b"Device 11:11:11:11:11:11 Keyboard\nDevice 22:22:22:22:22:22 TV\n"
        d._run_bluetoothctl.side_effect = [
            (0, paired_stdout2, b""),
            (0, b"Connected: no\nIcon: input-keyboard\n", b""),  # Keyboard peripheral
            (0, b"Connected: no\nIcon: video-display\n", b""),  # TV
            (0, b"", b""),  # connect TV
        ]
        results = asyncio.run(d._check_and_reconnect_bluetooth())
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["mac"], "22:22:22:22:22:22")


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
        self.assertEqual(cfg.tap_duration, 0.015)

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
tap_duration: 0.02
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
            self.assertEqual(cfg.tap_duration, 0.02)
        finally:
            os.remove(temp_path)

    def test_load_from_scoped_yaml(self):
        from config import load_config
        import tempfile
        content = """
remote:
  chromecast_addr: 10.0.0.50:8009
  cert_dir: /data/certs
  touch_device: /dev/input/event3
  http_port: 8092
  advert_name: Mirrormere Remote
  auto_confirm_pairing: true
  tap_duration: 0.01
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.chromecast_host, "10.0.0.50")
            self.assertEqual(cfg.cert_dir, "/data/certs")
            self.assertTrue(cfg.auto_confirm_pairing)
            self.assertEqual(cfg.tap_duration, 0.01)
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

    def test_managed_devices_and_pointer_mode_config(self):
        from config import RemoteConfig, load_config
        import tempfile
        content = """
managed_devices:
  - "aa:bb:cc:dd:ee:01"
  - "AA:BB:CC:DD:EE:02"
pointer_mode: "mouse"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.managed_devices, ["AA:BB:CC:DD:EE:01", "AA:BB:CC:DD:EE:02"])
            self.assertEqual(cfg.pointer_mode, "mouse")
        finally:
            os.remove(temp_path)

        # Test comma-separated string YAML and lowercase normalization
        content2 = """
managed_devices: "11:22:33:44:55:66, 77:88:99:aa:bb:cc"
pointer_mode: "TOUCH"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(content2)
            temp_path2 = f.name
        try:
            cfg2 = load_config(temp_path2)
            self.assertEqual(cfg2.managed_devices, ["11:22:33:44:55:66", "77:88:99:AA:BB:CC"])
            self.assertEqual(cfg2.pointer_mode, "touch")
        finally:
            os.remove(temp_path2)

        # Test env var override
        with patch.dict(os.environ, {
            "MIRRORMERE_REMOTE_MANAGED_DEVICES": "de:ad:be:ef:00:01, de:ad:be:ef:00:02",
            "MIRRORMERE_REMOTE_POINTER_MODE": "mouse",
        }):
            cfg_env = load_config("/nonexistent/file.yaml")
            self.assertEqual(cfg_env.managed_devices, ["DE:AD:BE:EF:00:01", "DE:AD:BE:EF:00:02"])
            self.assertEqual(cfg_env.pointer_mode, "mouse")


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
        # Report 2 Y axis Logical Maximum must be 18431 (0x47FF little-endian: 0xFF, 0x47)
        self.assertIn(bytes([0x09, 0x31, 0x15, 0x00, 0x26, 0xff, 0x47]), daemon.REPORT_MAP)
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
            self.daemon.hid.send_mouse.assert_not_called()
            self.assertEqual(self.daemon.hid.send_touch.call_count, 2)
            self.daemon.hid.send_touch.assert_any_call(True, 0.25, 0.75)
            self.daemon.hid.send_touch.assert_any_call(False, 0.25, 0.75)

    def test_handle_touch_down_move_up(self):
        for action, tip_down in [
            ("down", True),
            ("move", True),
            ("up", False),
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
            self.daemon.hid.send_mouse.assert_not_called()
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

        # Under default pointer_mode=touch, only touch release called
        self.daemon.hid.send_mouse.assert_not_called()
        self.daemon.hid.send_touch.assert_any_call(False, 0.5, 0.5)

    def test_handle_touch_and_tap_mouse_mode(self):
        self.daemon.config.pointer_mode = "mouse"
        # Test tap in mouse mode
        req = MagicMock()
        async def mock_tap_json():
            return {"x": 0.3, "y": 0.7}
        req.json = mock_tap_json

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_tap(req))
            self.assertEqual(resp.status, 200)
            self.daemon.hid.send_touch.assert_not_called()
            self.assertEqual(self.daemon.hid.send_mouse.call_count, 2)
            self.daemon.hid.send_mouse.assert_any_call(1, 0, 0)
            self.daemon.hid.send_mouse.assert_any_call(0, 0, 0)

        # Test touch actions in mouse mode
        for action, expect_mouse, mouse_arg in [
            ("down", True, 1),
            ("move", False, None),
            ("up", True, 0),
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
                self.daemon.hid.send_touch.assert_not_called()
                if expect_mouse:
                    self.daemon.hid.send_mouse.assert_called_once_with(mouse_arg, 0, 0)
                else:
                    self.daemon.hid.send_mouse.assert_not_called()

    def test_handle_touch_tap_duration(self):
        self.daemon.config.tap_duration = 0.012
        req = MagicMock()
        async def mock_json():
            return {"action": "tap", "x": 0.5, "y": 0.5}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web, patch("asyncio.sleep") as mock_sleep:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_touch(req))
            self.assertEqual(resp.status, 200)
            mock_sleep.assert_called_once_with(0.012)

    def test_handle_tap_duration(self):
        self.daemon.config.tap_duration = 0.018
        req = MagicMock()
        async def mock_json():
            return {"x": 0.5, "y": 0.5}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web, patch("asyncio.sleep") as mock_sleep:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_tap(req))
            self.assertEqual(resp.status, 200)
            mock_sleep.assert_called_once_with(0.018)

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


class TestBleBulletproofing(unittest.TestCase):
    def test_wake_keycodes(self):
        self.assertEqual(lookup_remote_key("wake"), "WAKEUP")
        self.assertEqual(lookup_remote_key("WAKEUP"), "WAKEUP")
        hid, remote = resolve_key_dispatch("wake")
        self.assertIsNone(hid)
        self.assertEqual(remote, "WAKEUP")

    def test_remote_config_heartbeat(self):
        from config import RemoteConfig, load_config
        cfg = RemoteConfig()
        self.assertTrue(cfg.bluetooth_heartbeat)
        self.assertEqual(cfg.bluetooth_heartbeat_interval, 30.0)

        import tempfile
        yaml_content = """
remote:
  bluetooth_heartbeat: false
  bluetooth_heartbeat_interval: 45.5
"""
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            parsed = load_config(temp_path)
            self.assertFalse(parsed.bluetooth_heartbeat)
            self.assertEqual(parsed.bluetooth_heartbeat_interval, 45.5)
        finally:
            os.remove(temp_path)

    def test_battery_service_send_level(self):
        if not daemon.HIDService:
            d = daemon.RemoteDaemon()
            d.send_heartbeat()
            return

        service = daemon.BatteryService()
        mock_changed = MagicMock()
        service.battery_level.changed = mock_changed
        service.send_battery_level(85)
        mock_changed.assert_called_once_with(bytes([85]))

        service.battery_level.changed.side_effect = Exception("D-Bus error")
        service.send_battery_level(100)

    def test_wake_via_wifi_if_needed(self):
        d = daemon.RemoteDaemon()
        d.remote_helper.connected = True
        d.remote_helper.remote = MagicMock()
        d.last_known_paired_devices = [{"mac": "DC:E5:5B:A6:30:8B", "connected": False}]

        async def run_test():
            await d._wake_via_wifi_if_needed()
            d.remote_helper.remote.send_key_command.assert_called_with("WAKEUP")

            d.remote_helper.remote.send_key_command.reset_mock()
            await d._wake_via_wifi_if_needed()
            d.remote_helper.remote.send_key_command.assert_not_called()

            d._last_wake_pulse = 0.0
            d.last_known_paired_devices = [{"mac": "DC:E5:5B:A6:30:8B", "connected": True}]
            await d._wake_via_wifi_if_needed()
            d.remote_helper.remote.send_key_command.assert_not_called()

        asyncio.run(run_test())

    def test_touch_lock_serialization(self):
        d = daemon.RemoteDaemon()
        d.hid = MagicMock()
        d.last_known_paired_devices = [{"mac": "DC:E5:5B:A6:30:8B", "connected": True}]

        order = []
        async def fake_tap(val, x, y):
            req = MagicMock()
            async def mock_json():
                return {"x": x, "y": y}
            req.json = mock_json
            def recorded_touch(*args):
                order.append(val)
            d.hid.send_touch = recorded_touch
            await d.handle_tap(req)

        async def run_concurrent():
            with patch.object(daemon, "web") as mock_web:
                mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
                await asyncio.gather(fake_tap("tap1", 0.5, 0.5), fake_tap("tap2", 0.3, 0.7))

        asyncio.run(run_concurrent())
        self.assertTrue(len(order) >= 2)

    def test_touch_lock_timeout(self):
        d = daemon.RemoteDaemon()
        d.hid = MagicMock()
        d.last_known_paired_devices = [{"mac": "DC:E5:5B:A6:30:8B", "connected": True}]

        async def run_timeout():
            req = MagicMock()
            async def mock_json():
                return {"action": "tap", "x": 0.5, "y": 0.5}
            req.json = mock_json

            with patch.object(daemon, "web") as mock_web, patch("asyncio.wait_for", side_effect=asyncio.TimeoutError):
                mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
                d.touch_lock = MagicMock()
                resp = await d.handle_touch(req)
                self.assertEqual(resp.status, 503)
                self.assertIn("timeout", resp.data.get("message", ""))

        asyncio.run(run_timeout())

    def test_reconnect_endpoint_wake_wifi(self):
        d = daemon.RemoteDaemon()
        d.remote_helper.connected = True
        d.remote_helper.remote = MagicMock()
        d.last_known_paired_devices = [{"mac": "DC:E5:5B:A6:30:8B", "connected": False}]

        req = MagicMock()
        async def mock_json():
            return {"mac": "DC:E5:5B:A6:30:8B", "wake_wifi": True}
        req.json = mock_json

        with patch.object(daemon, "web") as mock_web, patch.object(d, "_check_and_reconnect_bluetooth", new_callable=AsyncMock) as mock_reconnect:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            mock_reconnect.return_value = [{"mac": "DC:E5:5B:A6:30:8B", "reconnected": True}]
            resp = asyncio.run(d.handle_bluetooth_reconnect(req))
            self.assertEqual(resp.status, 200)
            d.remote_helper.remote.send_key_command.assert_called_with("WAKEUP")

    def test_android_remote_helper_send_voice(self):
        helper = daemon.AndroidRemoteHelper("127.0.0.1", "/tmp/nonexistent-certs-dir")
        # Disconnected
        self.assertFalse(asyncio.run(helper.send_voice(b"pcm-data")))

        # Connected with valid voice stream
        helper.connected = True
        mock_remote = MagicMock()
        mock_stream = AsyncMock()
        mock_stream.__aenter__.return_value = mock_stream
        mock_stream.send_chunk = MagicMock(return_value=True)
        mock_remote.start_voice = AsyncMock(return_value=mock_stream)
        helper.remote = mock_remote

        success = asyncio.run(helper.send_voice(b"pcm-data", timeout=1.5))
        self.assertTrue(success)
        mock_remote.start_voice.assert_awaited_once_with(timeout=1.5)
        mock_stream.send_chunk.assert_called_once_with(b"pcm-data")

        # start_voice error handling
        mock_remote.start_voice = AsyncMock(side_effect=RuntimeError("connection error"))
        self.assertFalse(asyncio.run(helper.send_voice(b"pcm-data")))


class TestVoiceEndpoint(unittest.TestCase):
    def setUp(self):
        self.daemon = daemon.RemoteDaemon()
        self.daemon.remote_helper.connected = True
        self.daemon.remote_helper.send_voice = AsyncMock(return_value=True)

    def test_handle_voice_empty_body(self):
        req = MagicMock()
        req.headers = {"Content-Type": "audio/wav"}
        req.query = {}
        async def mock_read():
            return b""
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 400)
            self.assertIn("no audio payload", resp.data["message"])

    def test_handle_voice_raw_pcm_success(self):
        raw_pcm = b"\x00\x01\x00\x02" * 4000  # 16000 bytes (16kHz 16-bit mono)
        req = MagicMock()
        req.headers = {"Content-Type": "audio/l16"}
        req.query = {}
        async def mock_read():
            return raw_pcm
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["status"], "ok")
            # Auto-decimated 2:1 to 8000 Hz
            self.assertEqual(resp.data["bytes_sent"], 8000)
            self.assertEqual(resp.data["sample_rate"], 8000)
            expected_decimated = b"".join([raw_pcm[i:i+2] for i in range(0, len(raw_pcm), 4)])
            self.daemon.remote_helper.send_voice.assert_awaited_once_with(expected_decimated)

    def test_handle_voice_explicit_16000(self):
        raw_pcm = b"\x00\x01" * 8000  # 16000 bytes
        req = MagicMock()
        req.headers = {"Content-Type": "audio/l16"}
        req.query = {"resample": "16000"}
        async def mock_read():
            return raw_pcm
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["bytes_sent"], 16000)
            self.assertEqual(resp.data["sample_rate"], 16000)
            self.daemon.remote_helper.send_voice.assert_awaited_once_with(raw_pcm)

    def test_handle_voice_wav_unwrapping(self):
        import io, wave
        raw_pcm = b"\x10\x00\x20\x00" * 2000
        buf = io.BytesIO()
        with wave.open(buf, "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(16000)
            wf.writeframes(raw_pcm)
        wav_bytes = buf.getvalue()

        req = MagicMock()
        req.headers = {"Content-Type": "audio/wav"}
        req.query = {}
        async def mock_read():
            return wav_bytes
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 200)
            # Default auto-decimated to 8000 Hz
            self.assertEqual(resp.data["bytes_sent"], 4000)
            self.assertEqual(resp.data["sample_rate"], 8000)
            expected_decimated = b"".join([raw_pcm[i:i+2] for i in range(0, len(raw_pcm), 4)])
            self.daemon.remote_helper.send_voice.assert_awaited_once_with(expected_decimated)

    def test_handle_voice_resample_8000(self):
        raw_pcm = b"\x00\x01\x00\x02" * 2000  # 8000 bytes of 16-bit samples
        req = MagicMock()
        req.headers = {"Content-Type": "audio/l16"}
        req.query = {"resample": "8000"}
        async def mock_read():
            return raw_pcm
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 200)
            self.assertEqual(resp.data["sample_rate"], 8000)
            self.assertEqual(resp.data["bytes_sent"], 4000)
            expected_decimated = b"".join([raw_pcm[i:i+2] for i in range(0, len(raw_pcm), 4)])
            self.daemon.remote_helper.send_voice.assert_awaited_once_with(expected_decimated)

    def test_handle_voice_disconnected_503(self):
        self.daemon.remote_helper.connected = False
        req = MagicMock()
        req.headers = {"Content-Type": "audio/l16"}
        req.query = {}
        async def mock_read():
            return b"some-pcm-audio"
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 503)
            self.assertEqual(resp.data["connected"], False)

    def test_handle_voice_delivery_failure_502(self):
        self.daemon.remote_helper.send_voice = AsyncMock(return_value=False)
        req = MagicMock()
        req.headers = {"Content-Type": "audio/l16"}
        req.query = {}
        async def mock_read():
            return b"some-pcm-audio"
        req.read = mock_read

        with patch.object(daemon, "web") as mock_web:
            mock_web.json_response = lambda data, status=200: MagicMock(status=status, data=data)
            resp = asyncio.run(self.daemon.handle_voice(req))
            self.assertEqual(resp.status, 502)


if __name__ == "__main__":
    unittest.main()

